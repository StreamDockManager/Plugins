"""Profile owned Windows plugin processes against a disposable loopback host.

No actions are pressed, external service credentials supplied, or user settings read.
Idle runs cover all plugins; --active only binds the listed read-only/local fixtures.
Optional Go instrumentation is injected through a build overlay, never shipped.
"""
import argparse
import base64
import ctypes as c
from ctypes import wintypes as w
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import socket
import struct
import subprocess
import threading
import time

ROOT = Path(__file__).resolve().parents[1]
READ_ONLY = {
    'counter-tally': ('increment', {}),
    'timers-pomodoro': ('countdown', {}),
    'randomizer-dice': ('dice', {}),
    'battery-power': ('status', {}),
    'network-vpn': ('vpn', {}),
    'system-monitor': ('cpu', {}),
    'spotify': ('status', {}),
}


class Counters(c.Structure):
    _fields_ = [('cb', w.DWORD), ('PageFaultCount', w.DWORD)] + [
        (key, c.c_size_t) for key in ('PeakWorkingSetSize', 'WorkingSetSize',
        'QuotaPeakPagedPoolUsage', 'QuotaPagedPoolUsage', 'QuotaPeakNonPagedPoolUsage',
        'QuotaNonPagedPoolUsage', 'PagefileUsage', 'PeakPagefileUsage', 'PrivateUsage')]


class ProcessEntry(c.Structure):
    _fields_ = [('dwSize', w.DWORD), ('cntUsage', w.DWORD), ('th32ProcessID', w.DWORD),
                ('th32DefaultHeapID', c.c_size_t), ('th32ModuleID', w.DWORD),
                ('cntThreads', w.DWORD), ('th32ParentProcessID', w.DWORD),
                ('pcPriClassBase', w.LONG), ('dwFlags', w.DWORD), ('szExeFile', w.WCHAR * 260)]


class Region(c.Structure):
    _fields_ = [('BaseAddress', c.c_void_p), ('AllocationBase', c.c_void_p),
                ('AllocationProtect', w.DWORD), ('PartitionId', w.WORD),
                ('RegionSize', c.c_size_t), ('State', w.DWORD), ('Protect', w.DWORD),
                ('Type', w.DWORD)]


class WorkingPage(c.Structure):
    _fields_ = [('address', c.c_void_p), ('attributes', c.c_size_t)]


# Load Windows process APIs and declare their ctypes argument signatures
def windows():
    kernel = c.WinDLL('kernel32', use_last_error=True)
    psapi = c.WinDLL('psapi', use_last_error=True)
    kernel.OpenProcess.argtypes, kernel.OpenProcess.restype = [w.DWORD, w.BOOL, w.DWORD], w.HANDLE
    kernel.CloseHandle.argtypes = [w.HANDLE]
    kernel.CreateToolhelp32Snapshot.argtypes = [w.DWORD, w.DWORD]
    kernel.CreateToolhelp32Snapshot.restype = w.HANDLE
    for name in ('Process32FirstW', 'Process32NextW'):
        getattr(kernel, name).argtypes = [w.HANDLE, c.POINTER(ProcessEntry)]
    kernel.VirtualQueryEx.argtypes = [w.HANDLE, c.c_void_p, c.POINTER(Region), c.c_size_t]
    kernel.VirtualQueryEx.restype = c.c_size_t
    psapi.GetProcessMemoryInfo.argtypes = [w.HANDLE, c.POINTER(Counters), w.DWORD]
    psapi.QueryWorkingSetEx.argtypes = [w.HANDLE, c.c_void_p, w.DWORD]
    psapi.GetMappedFileNameW.argtypes = [w.HANDLE, c.c_void_p, w.LPWSTR, w.DWORD]
    return kernel, psapi


# Enumerate the native processes included in memory accounting
def processes(kernel):
    result = {}
    snapshot = kernel.CreateToolhelp32Snapshot(2, 0)
    if snapshot == c.c_void_p(-1).value:
        raise c.WinError(c.get_last_error())
    try:
        item = ProcessEntry(dwSize=c.sizeof(ProcessEntry))
        more = kernel.Process32FirstW(snapshot, c.byref(item))
        while more:
            result[item.th32ProcessID] = (item.th32ParentProcessID, item.szExeFile, item.cntThreads)
            more = kernel.Process32NextW(snapshot, c.byref(item))
    finally:
        kernel.CloseHandle(snapshot)
    return result


# Read the memory counters for the selected native process
def memory(kernel, psapi, pid, breakdown=False):
    handle = kernel.OpenProcess(0x410, False, pid)
    if not handle:
        raise c.WinError(c.get_last_error())
    try:
        stats = Counters(cb=c.sizeof(Counters))
        if not psapi.GetProcessMemoryInfo(handle, c.byref(stats), c.sizeof(stats)):
            raise c.WinError(c.get_last_error())
        result = {'pid': pid, 'rss': stats.WorkingSetSize,
                  'private_commit': stats.PrivateUsage, 'peak_rss': stats.PeakWorkingSetSize}
        if breakdown:
            groups = {}
            address, region = 0, Region()
            while kernel.VirtualQueryEx(handle, address, c.byref(region), c.sizeof(region)):
                start, size = region.BaseAddress or 0, region.RegionSize
                if region.State == 0x1000 and not region.Protect & (0x100 | 1):
                    kind = {0x1000000: 'image', 0x20000: 'private', 0x40000: 'mapped'}.get(region.Type, 'other')
                    name = kind
                    if kind == 'image':
                        path = c.create_unicode_buffer(1024)
                        psapi.GetMappedFileNameW(handle, start, path, len(path))
                        name = path.value.rsplit('\\', 1)[-1]
                    group = groups.setdefault(name, {'kind': kind, 'commit': 0, 'resident': 0, 'resident_private': 0})
                    group['commit'] += size
                    for chunk in range(start, start + size, 4096 * 8192):
                        pages = (WorkingPage * min(8192, (start + size - chunk + 4095) // 4096))()
                        for i, page in enumerate(pages):
                            page.address = chunk + i * 4096
                        if not psapi.QueryWorkingSetEx(handle, pages, c.sizeof(pages)):
                            raise c.WinError(c.get_last_error())
                        for page in pages:
                            if page.attributes & 1:
                                group['resident'] += 4096
                                if not page.attributes & (1 << 15):
                                    group['resident_private'] += 4096
                address = start + size
            result['regions'] = groups
        return result
    finally:
        kernel.CloseHandle(handle)


class Host:
    # Initialize the disposable host used for process-memory measurement
    def __init__(self):
        self.listener = socket.socket()
        self.listener.bind(('127.0.0.1', 0))
        self.listener.listen(1)
        self.listener.settimeout(20)
        self.port = self.listener.getsockname()[1]
        self.connection = None
        self.lock = threading.Lock()
        self.ready = threading.Event()
        self.messages = []
        self.errors = []
        self.global_settings = {}
        self.thread = threading.Thread(target=self.serve, daemon=True)
        self.thread.start()

    # Send the fixture host message over the established connection
    def send(self, value):
        data = json.dumps(value, separators=(',', ':')).encode()
        header = bytes([0x81, len(data)]) if len(data) < 126 else b'\x81\x7e' + struct.pack('!H', len(data))
        with self.lock:
            self.connection.sendall(header + data)

    # Receive the next plugin message from the fixture connection
    def receive(self, n):
        result = b''
        while len(result) < n:
            part = self.connection.recv(n - len(result))
            if not part:
                raise EOFError()
            result += part
        return result

    # Serve the disposable host connection used by the memory workload
    def serve(self):
        try:
            self.connection, _ = self.listener.accept()
            data = b''
            while b'\r\n\r\n' not in data:
                data += self.receive(1)
            key = next(line.split(b':', 1)[1].strip() for line in data.split(b'\r\n') if line.lower().startswith(b'sec-websocket-key:'))
            accept = base64.b64encode(hashlib.sha1(key + b'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest())
            self.connection.sendall(b'HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + b'\r\n\r\n')
            while True:
                op, length = self.receive(2)
                masked, length = bool(length & 128), length & 127
                if length == 126:
                    length = struct.unpack('!H', self.receive(2))[0]
                elif length == 127:
                    length = struct.unpack('!Q', self.receive(8))[0]
                if length > 1 << 20:
                    raise ValueError('oversized plugin message')
                mask = self.receive(4) if masked else b''
                payload = self.receive(length)
                if masked:
                    payload = bytes(value ^ mask[i % 4] for i, value in enumerate(payload))
                if op & 15 == 8:
                    return
                if op & 15 != 1:
                    continue
                message = json.loads(payload)
                self.messages.append(message)
                event = message['event']
                if event == 'registerPlugin':
                    self.ready.set()
                elif event == 'getGlobalSettings':
                    self.send({'event': 'didReceiveGlobalSettings', 'payload': {'settings': self.global_settings}})
                elif event == 'setGlobalSettings':
                    self.global_settings = message['payload']
                elif event == 'getSettings':
                    self.send({'event': 'didReceiveSettings', 'context': message['context'], 'payload': {'settings': {}}})
        except (EOFError, OSError):
            pass
        except Exception as error:
            self.errors.append(str(error))

    # Stop the fixture host and release its owned connection resources
    def close(self):
        if self.connection:
            self.connection.close()
        self.listener.close()
        self.thread.join(2)


# Compile the selected plugin binary for the memory workload
def build_binary(slug, output, env, args):
    source = ROOT / 'cmd' / slug / 'main.go'
    injected = output / (slug + '-main.go.txt')
    extra = '''
func init() {
    path := _profileos.Getenv("SDM_MEMORY_PROFILE")
    if path == "" { return }
    go func() {
        for i := 0; ; i++ {
            _profiletime.Sleep(_profiletime.Second)
            var stats _profileruntime.MemStats
            _profileruntime.ReadMemStats(&stats)
            value := map[string]any{"second":i+1, "stats":stats, "gomaxprocs":_profileruntime.GOMAXPROCS(0), "goroutines":_profileruntime.NumGoroutine()}
            file, err := _profileos.OpenFile(path, _profileos.O_CREATE|_profileos.O_WRONLY|_profileos.O_APPEND, 0600)
            if err == nil { _profilejson.NewEncoder(file).Encode(value); file.Close() }
            if i == 4 {
                file, err := _profileos.Create(path+".heap")
                if err == nil { _profilepprof.WriteHeapProfile(file); file.Close() }
            }
        }
    }()
}
'''
    imports = '''import (
_profileos "os"
_profiletime "time"
_profileruntime "runtime"
_profilejson "encoding/json"
_profilepprof "runtime/pprof"
'''
    if args.instrument:
        injected.write_text(source.read_text().replace('import (', imports, 1) + extra)
    overlay = output / (slug + '-overlay.json')
    replacements = json.loads(args.build_overlay.read_text())['Replace'] if args.build_overlay else {}
    if args.instrument:
        replacements[str(source)] = str(injected)
    overlay.write_text(json.dumps({'Replace': replacements}))
    binary = output / (slug + '-plugin.exe')
    production = ROOT / 'dist' / 'bin' / 'windows' / 'amd64' / (slug + '-plugin.exe')
    descriptor = re.search(rb'SDMCAP1:[0-9a-f]{8}:[A-Za-z0-9+/=]+:ENDSDMCAP1', production.read_bytes())
    if descriptor is None:
        raise RuntimeError('missing production descriptor: ' + slug)
    flags = '-s -w' + (' -H=windowsgui' if args.gui else '')
    flags += ' -X github.com/StreamDockManager/Plugin-SDK/Go.CompiledDescriptor=' + descriptor[0].decode()
    compiler = str(args.go_executable or 'go')
    build_env = env.copy()
    if args.go_executable:
        build_env.update(GOROOT=str(args.go_executable.absolute().parents[1]), GOTOOLCHAIN='local')
    subprocess.run([compiler, 'build', '-trimpath', '-ldflags=' + flags, '-overlay', str(overlay), '-o', str(binary), './cmd/' + slug], cwd=ROOT, env=build_env, check=True)
    return binary


# Run the native memory workload and collect process observations
def run(slug, binary, args, index, output, env):
    host = Host()
    capabilities = ['plugin-protocol-v1', 'plugin-theme-v1', 'plugin-settings-v1', 'manager-controls-v1', 'action-results-v1', 'plugin-logs-v1', 'telemetry-v1']
    info = json.dumps({'streamDockManager': {'version': '0.1.0', 'contract_version': 1, 'capabilities': capabilities}})
    stderr_path = output / f'{slug}-{index}.stderr.txt'
    child_env = env.copy()
    if args.instrument:
        child_env['SDM_MEMORY_PROFILE'] = str(output / f'{slug}-{index}.go.jsonl')
    for value in ('GOGC', 'GOMEMLIMIT', 'GOMAXPROCS', 'GODEBUG'):
        child_env.pop(value, None)
    child_env.update(dict(item.split('=', 1) for item in args.env))
    kernel, psapi = windows()
    samples = []
    with stderr_path.open('w') as stderr:
        proc = subprocess.Popen([str(binary), '-port', str(host.port), '-pluginUUID', 'memory-fixture', '-info', info], cwd=output, env=child_env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=stderr, creationflags=0x08000000)
        try:
            if not host.ready.wait(15):
                raise RuntimeError(f'{slug} did not register: {stderr_path.read_text()}')
            if args.active:
                action, settings = READ_ONLY[slug]
                for i in range(args.bindings):
                    host.send({'event': 'willAppear', 'action': f'app.streamdockmanager.plugins.{slug}.{action}', 'context': f'fixture-{i}', 'device': 'fixture', 'payload': {'settings': settings}, 'streamDockManager': {'control_id': f'key-{i}', 'width': 72, 'height': 72, 'feedback_target': 'key'}})
            started = time.monotonic()
            while time.monotonic() - started < args.seconds:
                if proc.poll() is not None:
                    raise RuntimeError(f'{slug} exited: {stderr_path.read_text()}')
                tree = processes(kernel)
                owned = {proc.pid}
                while True:
                    found = {pid for pid, item in tree.items() if item[0] in owned} | owned
                    if found == owned:
                        break
                    owned = found
                members = []
                for pid in owned:
                    try:
                        value = memory(kernel, psapi, pid)
                        value['name'], value['threads'] = tree[pid][1:]
                        members.append(value)
                    except (OSError, KeyError):
                        pass  # A short-lived helper may exit between the two native snapshots
                samples.append({'second': time.monotonic() - started, 'processes': members, 'tree_rss': sum(item['rss'] for item in members)})
                time.sleep(.1)
            detail = memory(kernel, psapi, proc.pid, True)
            host.send({'event': 'shutdown'})
            proc.wait(15)
            if proc.returncode:
                raise RuntimeError(f'{slug} shutdown failed: {stderr_path.read_text()}')
        finally:
            if proc.poll() is None:
                subprocess.run(['taskkill', '/PID', str(proc.pid), '/T', '/F'], capture_output=True)
                proc.wait(10)
            host.close()
    result = {'slug': slug, 'run': index, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'samples': samples, 'final': detail, 'messages': host.messages, 'host_errors': host.errors, 'stderr': stderr_path.read_text()}
    (output / f'{slug}-{index}.json').write_text(json.dumps(result, indent=2))
    peak = max(item['tree_rss'] for item in samples)
    print(f'{slug}: root={detail["rss"]/1048576:.1f} MiB tree_peak={peak/1048576:.1f} MiB private_commit={detail["private_commit"]/1048576:.1f} MiB', flush=True)
    return {'slug': slug, 'run': index, 'root_rss': detail['rss'], 'tree_peak_rss': peak, 'private_commit': detail['private_commit']}


# Run the selected native Windows memory-measurement workload
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plugins', nargs='+')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--seconds', type=float, default=6)
    parser.add_argument('--repeat', type=int, default=1)
    parser.add_argument('--active', action='store_true')
    parser.add_argument('--bindings', type=int, default=1)
    parser.add_argument('--instrument', action='store_true')
    parser.add_argument('--build-stock', action='store_true')
    parser.add_argument('--gui', action='store_true')
    parser.add_argument('--go-executable', type=Path)
    parser.add_argument('--build-overlay', type=Path)
    parser.add_argument('--binary-dir', type=Path)
    parser.add_argument('--env', action='append', default=[])
    args = parser.parse_args()
    if os.name != 'nt' or c.sizeof(c.c_void_p) != 8:
        parser.error('requires native 64-bit Windows')
    if args.seconds <= 0 or args.repeat < 1 or not 1 <= args.bindings <= 32:
        parser.error('seconds and repeats must be positive; bindings must be 1..32')
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    slugs = args.plugins or sorted(path.name for path in (ROOT / 'cmd').iterdir() if path.is_dir())
    if args.active and set(slugs) - READ_ONLY.keys():
        parser.error('active fixtures are limited to: ' + ', '.join(READ_ONLY))
    env = os.environ.copy()
    env['GOMAXPROCS'] = '4'
    env['GOCACHE'] = str(ROOT / '.cache' / 'go-build')
    records = []
    for slug in slugs:
        if slug not in {path.name for path in (ROOT / 'cmd').iterdir()}:
            parser.error('unknown plugin ' + slug)
        binary = build_binary(slug, output, env, args) if args.instrument or args.build_stock else (args.binary_dir or ROOT / 'dist' / 'bin' / 'windows' / 'amd64') / (slug + '-plugin.exe')
        binary = binary.resolve()
        for index in range(args.repeat):
            records.append(run(slug, binary, args, index, output, env))
    metadata = {'platform': platform.platform(), 'processor_count': os.cpu_count(), 'source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(), 'go': subprocess.check_output(['go', 'version'], cwd=ROOT, text=True).strip(), 'arguments': {key: str(value) if isinstance(value, Path) else value for key, value in vars(args).items()}, 'records': records}
    (output / 'summary.json').write_text(json.dumps(metadata, indent=2))


if __name__ == '__main__':
    main()
