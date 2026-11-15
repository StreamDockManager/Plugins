#!/usr/bin/env python3
"""Validate every locally produced plugin package against its source definition."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import stat
import zipfile

import catalog
from profile_validation import validate_profiles


# Inspect target architectures without executing cross-compiled payloads
def validate_executable(data, osname, architectures=None):
    """Inspect target architectures without executing cross-compiled payloads."""
    architectures = architectures or catalog.PLATFORMS.get(osname)
    catalog.artifact_architecture(osname, architectures)
    if osname == 'linux':
        if len(data) >= 64 and data[:6] == b'\x7fELF\x02\x01' and struct.unpack_from('<H', data, 18)[0] == 62:
            return
    elif osname == 'windows':
        if len(data) >= 64 and data[:2] == b'MZ':
            offset = struct.unpack_from('<I', data, 60)[0]
            if offset <= len(data) - 6 and data[offset:offset+4] == b'PE\0\0' and struct.unpack_from('<H', data, offset+4)[0] == 0x8664:
                return
    elif osname == 'macos' and len(data) >= 32 and data[:4] == bytes.fromhex('cffaedfe'):
        cpu = struct.unpack_from('<I', data, 4)[0]
        if architectures == ['amd64'] and cpu == 0x1000007 or architectures == ['arm64'] and cpu == 0x100000c:
            return
    elif osname == 'macos' and architectures == ['amd64', 'arm64'] and len(data) >= 48 and data[:4] in (bytes.fromhex('cafebabe'), bytes.fromhex('bebafeca')):
        endian = '>' if data[:4] == bytes.fromhex('cafebabe') else '<'
        count = struct.unpack_from(endian+'I', data, 4)[0]
        if count == 2:
            architectures, ranges = set(), []
            for index in range(count):
                cpu, _, offset, size, alignment = struct.unpack_from(endian+'IIIII', data, 8+index*20)
                if (offset < 48 or size < 32 or offset+size > len(data) or alignment > 31
                        or offset % (1 << alignment) or any(offset < end and start < offset+size for start, end in ranges)):
                    raise ValueError('Invalid universal Mach-O slice range')
                if data[offset:offset+4] != bytes.fromhex('cffaedfe') or struct.unpack_from('<I', data, offset+4)[0] != cpu or cpu in architectures:
                    raise ValueError('Invalid universal Mach-O slice architecture')
                architectures.add(cpu)
                ranges.append((offset, offset+size))
            if architectures == {0x1000007, 0x100000c}:
                return
    raise ValueError('Executable target architecture mismatch')


# Validate package contents against the declared source and target platform
def validate(directory=None, plan=None, all_platforms=False, records=None, universal=False, macos_intel=False, policy=None, validated_inventories=None):
    directory = Path(directory or catalog.ROOT / 'dist')
    source = {record['id']: record for record in (catalog.source_records() if records is None else records)}
    if plan is not None:
        catalog.verify_plan(plan)
        source = {identity: source[identity] for identity in plan['selected']}
    policy = policy or (plan['support_policy'] if plan else catalog.support_policy())
    catalog.check_source_policy(source.values(), policy)
    if universal and all_platforms: raise ValueError('Universal validation is separate from default platforms')
    catalogs = [directory / 'optional/catalog-macos-universal.json'] if universal else sorted(directory.glob('catalog-*.json'))
    if not catalogs:
        raise ValueError('No platform packages have been produced')
    if all_platforms and {path.name for path in catalogs} != {'catalog-linux.json', 'catalog-windows.json', 'catalog-macos.json'}:
        raise ValueError('Packages must cover exactly Linux, Windows, and macOS')
    count = 0
    for path in catalogs:
        platform_catalog = json.loads(path.read_text())
        if platform_catalog.get('support_policy') != policy:
            raise ValueError('Platform inventory support policy differs from source snapshot')
        if any(record.get('source_fingerprint') is not None and record['source_fingerprint'] != platform_catalog.get('source_fingerprint') for record in source.values()):
            raise ValueError('Platform inventory source fingerprint differs; rebuild plugins')
        expected_os = path.stem.removeprefix('catalog-').removesuffix('-universal')
        expected_ids = {identity for identity, definition in source.items() if expected_os in catalog.supported_platforms(definition)}
        if ({p['id'] for p in platform_catalog['packages']} != expected_ids
                or any(p['os'] != expected_os for p in platform_catalog['packages'])):
            raise ValueError('Platform catalog must contain exactly the selected source plugins')
        for identity in expected_ids:
            if all_platforms:
                catalog.selected_packages(platform_catalog['packages'], identity, expected_os, macos_intel=macos_intel)
        seen = set()
        for package in platform_catalog['packages']:
            variant = catalog.artifact_architecture(package['os'], package['architectures'])
            if universal and variant != 'universal': raise ValueError('Expected universal package')
            if (package['id'],variant) in seen: raise ValueError('Duplicate package variant')
            seen.add((package['id'],variant))
            definition = source[package['id']]
            osname = package['os']
            archive_path = catalog.package_path(directory, package)
            # A captured inventory authorizes reuse only for these exact bytes,
            # not a path that could be replaced between hashing and validation
            archive_input = archive_path
            if validated_inventories is not None:
                with archive_path.open('rb') as source_file:
                    raw = source_file.read(256 * 1024 * 1024 + 1)
                if len(raw) > 256 * 1024 * 1024:
                    raise ValueError('Unified source archive exceeds download limit')
                actual_digest, actual_size = hashlib.sha256(raw).hexdigest(), len(raw)
                archive_input = io.BytesIO(raw)
            else:
                actual_digest, actual_size = catalog.digest(archive_path), archive_path.stat().st_size
            if actual_digest != package['sha256'] or actual_size != package['size']:
                raise ValueError(f'{archive_path}: ZIP size/hash mismatch')
            with zipfile.ZipFile(archive_input) as archive:
                prefix = package['id'] + '.sdPlugin/'
                if validated_inventories is not None:
                    entries = archive.infolist()
                    names = [entry.filename.removeprefix(prefix).casefold() for entry in entries]
                    if (len(entries) > 4096 or len(set(names)) != len(names)
                            or sum(entry.file_size for entry in entries) > 256 * 1024 * 1024
                            or any(not entry.filename.startswith(prefix)
                                   or not catalog.safe_path(entry.filename.removeprefix(prefix))
                                   or entry.is_dir() or entry.flag_bits & 1
                                   or stat.S_IFMT(entry.external_attr >> 16) not in (0, stat.S_IFREG)
                                   for entry in entries)):
                        raise ValueError('Unsafe or oversized unified input package')
                manifest = json.loads(archive.read(prefix + 'manifest.json'))
                validate_profiles(archive, prefix, definition)
                code_bytes = catalog.validate_compiled_capabilities(archive, prefix, manifest['CodePath'], definition['capabilities'])
                files = {}
                for entry in archive.infolist():
                    if not entry.filename.startswith(prefix) or not catalog.safe_path(entry.filename[len(prefix):]) or entry.is_dir() or entry.filename[len(prefix):] in files:
                        raise ValueError('Unsafe or duplicate package member')
                    files[entry.filename[len(prefix):]] = hashlib.sha256(archive.read(entry)).hexdigest()
                if files != package['files'] or len(files) != len(archive.infolist()):
                    raise ValueError('Complete file inventory mismatch')
                for key in ('Icon', 'CategoryIcon'):
                    if not catalog.safe_path(manifest.get(key)) or manifest[key] not in files:
                        raise ValueError('Package icon must name an exact packaged asset')
                catalog.validate_compression(archive, prefix, manifest['CodePath'])
                validate_executable(code_bytes, osname, package['architectures'])
                if osname != 'windows' and archive.getinfo(prefix + manifest['CodePath']).external_attr >> 16 & 0o111 != 0o111:
                    raise ValueError('Executable archive permissions are missing')
                platform_code = {'macos': 'CodePathMac', 'windows': 'CodePathWin', 'linux': 'CodePathLinux'}[osname]
                code = manifest['CodePath']
                if manifest['SDKVersion'] != 1 or manifest.get(platform_code) != code or code not in files or manifest['UUID'] != definition['id'] or manifest['Version'] != definition['version']:
                    raise ValueError('VSD SDK, identity, version, or code path mismatch')
                if manifest['Software']['MinimumVersion'] != '3.10.205.0918' or manifest['OS'] != [{'Platform': {'macos': 'mac', 'windows': 'windows', 'linux': 'linux'}[osname], 'MinimumVersion': {'macos': '14', 'windows': '11', 'linux': '0'}[osname]}]:
                    raise ValueError('Native platform/software floor mismatch')
                extension = manifest['StreamDockManager']
                if extension['ManifestDialect'] != 'vsd' or extension['Capabilities'] != definition['capabilities'] or extension['MinimumVersion'] != definition['minimum_manager'] or extension['RequiredCapabilities'] != definition['required_capabilities']:
                    raise ValueError('Compiled capability mirror/requirements mismatch')
                for action in manifest['Actions']:
                    if action['UUID'] != action['UUID'].lower() or action['UUID'] not in definition['capabilities']['actions'] or action.get('Encoder'):
                        raise ValueError('VSD action/controller mismatch')
                    capability = definition['capabilities']['actions'][action['UUID']]
                    types = capability['control_types']
                    expected = ['Keypad', 'Knob'] if types == ['button', 'encoder', 'touch'] else [{'button': 'Keypad', 'encoder': 'Knob', 'touch': 'Information'}[kind] for kind in types]
                    if action['Controllers'] != expected:
                        raise ValueError('VSD controllers differ from action capabilities')
                    if capability['feedback_layout'] not in files:
                        raise ValueError('Manager feedback layout missing')
                    for asset in [action['Icon'], *(state['Image'] for state in action['States'])]:
                        if not catalog.safe_path(asset) or asset not in files:
                            raise ValueError('Action icon/state path missing or unsafe')
            count += 1
        if validated_inventories is not None:
            validated_inventories[expected_os] = json.dumps(platform_catalog, sort_keys=True)
        print(f'{path.name}: {len(platform_catalog["packages"])} manifests, compiled descriptors, inventories and requirements validated')
    print(f'{count} packages passed. Native VSD runtime compatibility remains unverified.')
    return count


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path)
    parser.add_argument('--all-platforms', action='store_true')
    parser.add_argument('--universal', action='store_true', help='validate explicit optional universal Mac packages')
    parser.add_argument('--macos-intel', action='store_true', default=os.environ.get('PLUGIN_MACOS_INTEL')=='1', help='require the optional Intel macOS package as well as Apple Silicon')
    args = parser.parse_args()
    validate(plan=json.loads(args.plan.read_text()) if args.plan else None, all_platforms=args.all_platforms, universal=args.universal, macos_intel=args.macos_intel)
