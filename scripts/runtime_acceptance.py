#!/usr/bin/env python3
"""Build and measure immutable Mac runtime snapshots without touching a checkout."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import tarfile
import time

ROOT = Path(__file__).resolve().parent.parent
HARNESS = Path('internal/integration/runtime_acceptance_test.go')
MICROPHONE_ADAPTER = Path('internal/plugins/microphone-controls/adapter.go')
CASES = ('idle', 'visible_shared', 'failing_unique', 'rapid_reconfiguration')
CURRENT_CASES = ('telemetry_expiry', 'persistence')


# Hash the snapshot file bytes with SHA-256
def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


# Own the entire compiler/test group, including the fixture child process
def run(command, *, cwd=ROOT, env=None, log=None):
    """Own the entire compiler/test group, including the fixture child process."""
    process = subprocess.Popen([str(x) for x in command], cwd=cwd, env=env,
                               stdout=log or subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, start_new_session=True)
    try:
        output, _ = process.communicate()
        if process.returncode:
            raise RuntimeError(f'{command[0]} exited {process.returncode}; see {log.name if log else output}')
        return (output or '').strip()
    finally:
        # A child may survive its leader. Address only this owned process group
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            pass
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=3)


# Record the files and digests belonging to an acceptance snapshot
def inventory(directory):
    return {path.relative_to(directory).as_posix(): digest(path)
            for path in sorted(directory.rglob('*')) if path.is_file()}


# Write the acceptance report without replacing an existing report
def save(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


# Construct the isolated environment used by the acceptance harness
def environment():
    result = os.environ.copy()
    for name in list(result):
        if name.startswith('STREAM_DOCK_MANAGER_ACCEPTANCE_'):
            del result[name]
    result.update(GOWORK='off', GOTOOLCHAIN='local', GOFLAGS='')
    return result


# Current acceptance requires the maintained explicit-factory layout
def snapshot_harness(source):
    """Current acceptance requires the maintained explicit-factory layout."""
    if not (source / MICROPHONE_ADAPTER).is_file():
        raise ValueError('Unsupported historical snapshot; use its matching runtime acceptance tooling')
    return HARNESS, HARNESS, './internal/integration', 'microphone-factories'


# Resolve a source revision and validate its acceptance-test layout
def resolve_snapshot(ref):
    commit = run(['git', 'rev-parse', '--verify', ref + '^{commit}'])
    try:
        run(['git', 'cat-file', '-e', commit + ':' + MICROPHONE_ADAPTER.as_posix()])
    except RuntimeError as error:
        raise ValueError(f'Unsupported historical snapshot {ref}; use its matching runtime acceptance tooling') from error
    return commit


# Build isolated source snapshots for the requested acceptance comparison
def prepare(destination, before, after):
    # Validate both requested snapshots before creating output or invoking Go
    commits = {label: resolve_snapshot(ref) for label, ref in (('before', before), ('after', after))}
    destination.mkdir(parents=True, exist_ok=False)
    env = environment()
    # The PATH launcher may be older than the toolchain selected by this module
    # Resolve that compiler once, then use its executable for both full snapshots
    compiler = Path(run(['go', 'env', 'GOROOT'])) / 'bin/go'
    env['GOROOT'] = str(compiler.parent.parent)
    harness_bytes = (ROOT / HARNESS).read_bytes()
    harness_sha256 = hashlib.sha256(harness_bytes).hexdigest()
    record = {'schema_version': 1, 'status': 'preparing', 'host': platform.platform(),
              'machine': platform.machine(), 'toolchain': run([compiler, 'version'], env=env),
              'compiler': str(compiler),
              'harness_sha256': harness_sha256, 'snapshots': {},
              'requested_refs': {'before': before, 'after': after},
              'scope': 'Complete selected source snapshots with a layout-compatible test-only harness; each snapshot records the exact harness identity.',
              'limitations': ['Controlled delayed/error audio adapter; no native audio/device qualification.',
                             'CPU belongs to the child Engine/SDK test process, including instrumentation and lifecycle; host driver runs separately.',
                             'Host CPU/memory/disk/audio sampling cadence is Manager-owned and is not measured here.',
                             'Shared-host observations, not a controlled or portable performance guarantee. Go caches are retained.',
                             'Six-second error workload observes initial backoff; it does not wait through the full 60-second cap.']}
    save(destination / 'preparation.json', record)
    try:
        for label, ref in (('before', before), ('after', after)):
            commit = commits[label]
            archive = destination / (label + '.tar')
            run(['git', 'archive', '--format=tar', '--output', archive, commit])
            source = destination / label
            source.mkdir()
            with tarfile.open(archive) as stream:
                stream.extractall(source, filter='data')
            original = inventory(source)
            target_harness, fixture, package, kind = snapshot_harness(source)
            original_harness = original.get(target_harness.as_posix())
            (source / target_harness).parent.mkdir(parents=True, exist_ok=True)
            (source / target_harness).write_bytes(harness_bytes)
            binary = destination / (label + '-engine.test')
            command = [compiler, 'test', '-mod=readonly', '-p=4', '-trimpath', '-c', package, '-o', binary]
            with (destination / (label + '-compile.log')).open('w') as log:
                run(command, cwd=source, env=env, log=log)
            (destination / (label + '-modules.json')).write_text(run([compiler, 'list', '-mod=readonly', '-m', '-json', 'all'], cwd=source, env=env) + '\n')
            actual = inventory(source)
            expected = {**original, target_harness.as_posix(): harness_sha256}
            if actual != expected:
                raise ValueError('Compilation changed snapshot source files')
            record['snapshots'][label] = {'commit': commit, 'tree': run(['git', 'rev-parse', commit + '^{tree}']),
                                          'archive_sha256': digest(archive), 'binary_sha256': digest(binary),
                                          'original_files': original, 'original_harness_sha256': original_harness,
                                          'harness_path': target_harness.as_posix(), 'harness_fixture': fixture.as_posix(),
                                          'harness_sha256': harness_sha256, 'harness_kind': kind,
                                          'compile_command': [str(x) for x in command], 'files': expected}
            save(destination / 'preparation.json', record)
        record['same_harness'] = True
        record['status'] = 'prepared'
    except BaseException as error:
        record['status'] = 'failed'
        record['error'] = type(error).__name__ + ': ' + str(error)
        raise
    finally:
        save(destination / 'preparation.json', record)


# Require current preparation metadata before trusting its identities
def verify(destination, preparation):
    """Require current preparation metadata before trusting its identities."""
    unsupported = 'Unsupported preparation report; prepare current snapshots again'
    if not isinstance(preparation, dict) or preparation.get('schema_version') != 1:
        raise ValueError(unsupported)
    required = {'status', 'harness_sha256', 'same_harness', 'snapshots'}
    if not required.issubset(preparation) or preparation['same_harness'] is not True:
        raise ValueError(unsupported)
    snapshots = preparation['snapshots']
    if not isinstance(snapshots, dict) or set(snapshots) != {'before', 'after'}:
        raise ValueError(unsupported)
    if preparation['status'] != 'prepared' or not (ROOT / HARNESS).is_file() or digest(ROOT / HARNESS) != preparation['harness_sha256']:
        raise ValueError('Prepared harness identity no longer matches')
    for label, snapshot in snapshots.items():
        fields = {'harness_fixture', 'harness_path', 'harness_kind', 'harness_sha256', 'files', 'binary_sha256'}
        if not isinstance(snapshot, dict) or not fields.issubset(snapshot):
            raise ValueError(unsupported)
        if (snapshot['harness_fixture'] != HARNESS.as_posix() or snapshot['harness_path'] != HARNESS.as_posix()
                or snapshot['harness_kind'] != 'microphone-factories'):
            raise ValueError('Unsupported historical harness; prepare a current snapshot')
        if (snapshot['harness_sha256'] != preparation['harness_sha256']
                or not isinstance(snapshot['files'], dict)
                or snapshot['files'].get(HARNESS.as_posix()) != preparation['harness_sha256']):
            raise ValueError('Prepared harness identity no longer matches')
        if inventory(destination / label) != snapshot['files'] or digest(destination / (label + '-engine.test')) != snapshot['binary_sha256']:
            raise ValueError('Prepared source or executable identity changed')


# Run the prepared acceptance workload and record its measured results
def measure(destination, repeats):
    preparation = json.loads((destination / 'preparation.json').read_text())
    verify(destination, preparation)
    report_path = destination / 'report.json'
    if report_path.exists():
        raise ValueError('Refusing to overwrite an existing measurement report')
    report = {'schema_version': 1, 'status': 'running', 'preparation_sha256': digest(destination / 'preparation.json'),
              'repeats': repeats, 'same_harness': preparation['same_harness'], 'runs': [], 'host': platform.platform(), 'machine': platform.machine()}
    save(report_path, report)
    try:
        for repeat in range(repeats):
            # Alternate ordering so all later-source runs are not systematically warmer
            for label in (('before', 'after') if repeat % 2 == 0 else ('after', 'before')):
                for case in CASES + (CURRENT_CASES if label == 'after' else ()):
                    name = f'{repeat + 1}-{label}-{case}'
                    output = destination / (name + '.json')
                    env = environment()
                    env.update(STREAM_DOCK_MANAGER_ACCEPTANCE_REPORT=str(output), STREAM_DOCK_MANAGER_ACCEPTANCE_CASE=case)
                    command = [destination / (label + '-engine.test'), '-test.run=^TestRuntimeWorkloadAcceptance$', '-test.count=1', '-test.timeout=40s']
                    started = time.monotonic()
                    failure = None
                    with (destination / (name + '.log')).open('w') as log:
                        try:
                            run(command, cwd=destination / label, env=env, log=log)
                        except RuntimeError as error:
                            failure = str(error)
                    result = json.loads(output.read_text()) if output.is_file() else {}
                    report['runs'].append({'repeat': repeat + 1, 'revision': label, 'case': case, 'seconds': time.monotonic()-started,
                                           'report': output.name, 'report_sha256': digest(output) if output.is_file() else None,
                                           'passed': failure is None and result.get('passed') is True, 'error': failure})
                    save(report_path, report)
                    print(name, 'passed' if report['runs'][-1]['passed'] else 'FAILED', flush=True)
        verify(destination, preparation)
        report['source_and_binary_identity_preserved'] = True
        report['status'] = 'passed' if all(run['passed'] for run in report['runs']) else 'failed'
    except BaseException as error:
        report['status'] = 'interrupted' if isinstance(error, KeyboardInterrupt) else 'failed'
        report['error'] = type(error).__name__ + ': ' + str(error)
        raise
    finally:
        save(report_path, report)
    if report['status'] != 'passed':
        raise RuntimeError('At least one workload failed; inspect retained reports and logs')


# Dispatch acceptance snapshot preparation, verification, or measurement
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=('prepare', 'measure'))
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--before', help='explicit baseline revision (required for prepare)')
    parser.add_argument('--after', help='explicit comparison revision (required for prepare)')
    parser.add_argument('--repeats', type=int, default=3, choices=range(1, 6))
    args = parser.parse_args()
    if args.phase == 'prepare' and (not args.before or not args.after):
        parser.error('prepare requires explicit --before and --after revisions')
    if platform.system() != 'Darwin':
        parser.error('This acceptance harness is Mac-only; it does not qualify Windows/Linux')
    # Stop the measurement cleanly when the process receives an interrupt
    def interrupted(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    destination = args.output.resolve()
    if args.phase == 'prepare':
        prepare(destination, args.before, args.after)
    else:
        measure(destination, args.repeats)


if __name__ == '__main__':
    main()
