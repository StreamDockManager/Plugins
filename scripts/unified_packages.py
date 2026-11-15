#!/usr/bin/env python3
"""Create optional multi-OS bundles from verified, matching platform packages."""
import argparse
import hashlib
import io
import sys
import zlib
import json
import os
from pathlib import Path
import stat
import tempfile
import zipfile

import catalog
import validate_packages
import opendeck_export

TRIPLES = {
    ('windows', 'amd64'): 'x86_64-pc-windows-msvc',
    ('linux', 'amd64'): 'x86_64-unknown-linux-gnu',
    ('macos', 'amd64'): 'x86_64-apple-darwin',
    ('macos', 'arm64'): 'aarch64-apple-darwin',
}
CODE_FIELDS = ('CodePath', 'CodePathWin', 'CodePathMac', 'CodePathLinux', 'CodePathLin', 'CodePaths', 'OS')
NOTICES = ('THIRD_PARTY_LICENSES.txt', 'THIRD_PARTY_NOTICES.md')
LIMIT = 256 * 1024 * 1024


# Resolve the universal archive path for a plugin and target host
def path_for(directory, identity, host="manager"):
    if not isinstance(identity, str) or not catalog.REPO.fullmatch('plugins/' + identity):
        raise ValueError('Invalid unified package identity')
    directory = Path(directory).resolve()
    if host not in ('manager', 'opendeck'):
        raise ValueError('Unsupported package host')
    path = directory / 'packages' / ('opendeck' if host == 'opendeck' else 'unified') / (identity + '.zip')
    if not path.resolve().is_relative_to(directory):
        raise ValueError('Unified package escapes output directory')
    return path


# Read a validated package inventory while bounding archive contents
def read_archive(path, identity):
    prefix = identity + '.sdPlugin/'
    files = {}
    total = 0
    folded = set()
    with zipfile.ZipFile(path) as archive:
        if len(archive.infolist()) > 4096:
            raise ValueError('Too many unified input members')
        for entry in archive.infolist():
            name = entry.filename.removeprefix(prefix)
            total += entry.file_size
            mode = entry.external_attr >> 16
            if (not entry.filename.startswith(prefix) or not catalog.safe_path(name)
                    or name.casefold() in folded or entry.is_dir() or entry.flag_bits & 1
                    or stat.S_IFMT(mode) not in (0, stat.S_IFREG)
                    or total > LIMIT or entry.file_size > 256 * 1024 * 1024):
                raise ValueError('Unsafe or oversized unified package member')
            folded.add(name.casefold())
            files[name] = archive.read(entry)
    return files


# Reject different shared assets/manifests instead of silently picking one
def assemble(directory, record, inventories, host="manager", *, verified=None):
    """Reject different shared assets/manifests instead of silently picking one."""
    shared = None
    common_manifest = None
    binaries = {}
    notices = {}
    code_paths = {}
    targets = []
    floors = []
    for osname in sorted(catalog.PLATFORMS):
        packages = [p for p in inventories[osname]['packages'] if p['id'] == record['id']]
        if not packages:
            raise ValueError('Unified bundle requires all three operating systems')
        architectures = []
        for package in sorted(packages, key=lambda p: p['architectures']):
            if package['version'] != record['version'] or len(package['architectures']) != 1:
                raise ValueError('Unified inputs require matching versions and single-CPU packages')
            arch = package['architectures'][0]
            triple = TRIPLES.get((osname, arch))
            if triple is None or triple in code_paths:
                raise ValueError('Unsupported or duplicate unified target')
            path = catalog.package_path(directory, package)
            with path.open('rb') as source_file:
                raw = source_file.read(LIMIT + 1)
            if len(raw) > LIMIT:
                raise ValueError('Unified source archive exceeds download limit')
            if hashlib.sha256(raw).hexdigest() != package['sha256'] or len(raw) != package['size']:
                raise ValueError('Unified source archive changed')
            contents = read_archive(io.BytesIO(raw), record['id'])
            if {n: hashlib.sha256(b).hexdigest() for n, b in contents.items()} != package['files']:
                raise ValueError('Unified source inventory mismatch')
            manifest = json.loads(contents.pop('manifest.json'))
            if manifest['UUID'] != record['id'] or manifest['Version'] != record['version']:
                raise ValueError('Unified source manifest identity/version mismatch')
            entry = manifest['CodePath']
            binary = contents.pop(entry)
            if verified is None or not verified.matches(osname, package):
                with zipfile.ZipFile(io.BytesIO(raw)) as source_archive:
                    catalog.validate_compiled_capabilities(source_archive, record['id'] + '.sdPlugin/', entry, record['capabilities'])
                validate_packages.validate_executable(binary, osname, [arch])
            target = f'{osname}-{arch}'
            destination = f'bin/{target}/{Path(entry).name}'
            binaries[destination] = binary
            code_paths[triple] = destination
            architectures.append(arch)
            for name in NOTICES:
                if name in contents:
                    notices[f'licenses/{target}/{name}'] = contents.pop(name)
            if package == packages[0]:
                floors.extend(manifest['OS'])
            for field in CODE_FIELDS:
                manifest.pop(field, None)
            if shared is None:
                shared, common_manifest = contents, manifest
            elif contents != shared or manifest != common_manifest:
                raise ValueError('Platform packages have different shared assets or manifest metadata; rebuild together')
        targets.append({'os': osname, 'architectures': sorted(architectures)})
    common_manifest['CodePaths'] = code_paths
    common_manifest['OS'] = sorted(floors, key=lambda floor: floor['Platform'])
    if host == 'opendeck':
        # The three per-OS conversions must agree except for their OS floor
        converted = []
        for platform in ('mac', 'windows', 'linux'):
            item = opendeck_export.export_manifest(common_manifest, shared, platform, materialize_aliases=True)
            item.pop('OS')
            converted.append(item)
        if converted[1:] != converted[:1] * 2:
            raise ValueError('OpenAction manifest conversion differs across platforms')
        common_manifest = converted[0]
        common_manifest['OS'] = sorted(floors, key=lambda floor: floor['Platform'])
        for osname, field in [('macos', 'CodePathMac'), ('windows', 'CodePathWin'), ('linux', 'CodePathLin')]:
            for arch in ('amd64', 'arm64'):
                path = code_paths.get(TRIPLES.get((osname, arch)))
                if path:
                    common_manifest[field] = path
                    break
    elif host != 'manager':
        raise ValueError('Unsupported package host')
    generated = set(binaries) | set(notices) | {'manifest.json', 'THIRD_PARTY_LICENSES.txt'}
    if set(shared) & generated:
        raise ValueError('Unified generated path collides with shared assets')
    files = {**shared, **binaries, **notices}
    files['manifest.json'] = (json.dumps(common_manifest, indent=4, sort_keys=True) + '\n').encode()
    files['THIRD_PARTY_LICENSES.txt'] = ('Target-specific dependency licenses and notices are in licenses/<os>-<arch>/.\n').encode()
    names = {name.casefold() for name in files}
    if len(names) != len(files) or any('/'.join(name.split('/')[:i]) in names for name in names for i in range(1, len(name.split('/')))):
        raise ValueError('Unified output paths collide')
    if len(files) > 4096 or sum(map(len, files.values())) > LIMIT:
        raise ValueError('Unified bundle exceeds its bounded inventory')
    return files, set(binaries), targets


# Write deterministic ZIP members with the required compression and modes
def write_archive(path, identity, files, executables, host="manager"):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.unified-', suffix='.zip', dir=path.parent)
    os.close(fd)
    try:
        with zipfile.ZipFile(temporary, 'w') as archive:
            for name, data in sorted(files.items()):
                entry = zipfile.ZipInfo(identity + '.sdPlugin/' + name, (2026, 1, 1, 0, 0, 0))
                entry.create_system = 3
                entry.external_attr = (0o100755 if name in executables else 0o100644) << 16
                archive.writestr(entry, data, compress_type=zipfile.ZIP_DEFLATED, compresslevel=6)
        if Path(temporary).stat().st_size > LIMIT:
            raise ValueError('Unified archive exceeds download limit')
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


class VerifiedInputs:
    """Invocation-local immutable inventory, only produced after full validation.

    Assembly still hashes the exact bytes it opens, so replacing a source after
    validation cannot reuse semantic validation for different content.
    """
    # Retain encoded inventory snapshots and their validation scope
    def __init__(self, encoded, scope):
        self._encoded = tuple(sorted(encoded.items()))
        self._scope = scope

    # Decode fresh copies of the retained platform inventories
    def inventories(self):
        return {name: json.loads(value) for name, value in self._encoded}

    # Check whether package metadata is present in the validated inventory
    def matches(self, osname, package):
        return package in self.inventories()[osname]['packages']


# Identify the source and validation scope for reusable package inventories
def input_scope(directory, records, macos_intel):
    return (str(Path(directory).resolve()), json.dumps(records, sort_keys=True), bool(macos_intel))


# Load and validate the platform inputs required for universal packaging
def inputs(directory, records, macos_intel=False):
    records = list(records)
    validated = {}
    validate_packages.validate(Path(directory), records=records, all_platforms=True,
                               macos_intel=macos_intel, validated_inventories=validated)
    return VerifiedInputs(validated, input_scope(directory, records, macos_intel))


# Hash source, target, exporter, and compression inputs for artifact caching
def cache_key(record, inventories, host):
    sources = {name: catalog.digest(Path(__file__).with_name(name)) for name in
               ('unified_packages.py', 'opendeck_export.py', 'catalog.py', 'validate_packages.py', 'profile_validation.py')}
    packages = {osname: [p for p in inventory['packages'] if p['id'] == record['id']]
                for osname, inventory in inventories.items()}
    value = {'schema': 1, 'compression': 'deflate6', 'python': sys.version,
             'zlib': zlib.ZLIB_RUNTIME_VERSION, 'host': host, 'record': record,
             'packages': packages, 'implementation': sources}
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


# Local optimization only; independent validate() never trusts this cache
def cached_package(path, key, identity, files, executables, targets):
    """Local optimization only; independent validate() never trusts this cache."""
    try:
        if path.is_symlink() or path.with_suffix('.cache.json').is_symlink():
            return None
        with path.with_suffix('.cache.json').open() as stream:
            value = json.loads(stream.read(16 * 1024 * 1024))
        package = value['package']
        if set(package) != {'id', 'version', 'path', 'sha256', 'size', 'files', 'targets'}:
            return None
        digest = hashlib.sha256(json.dumps(package, sort_keys=True).encode()).hexdigest()
        if (value['key'] != key or value['metadata_sha256'] != digest
                or package['files'] != {n: hashlib.sha256(b).hexdigest() for n, b in files.items()}
                or package['targets'] != targets or package['id'] != identity
                or package['version'] != json.loads(files['manifest.json'])['Version']):
            return None
        with path.open('rb') as stream:
            raw = stream.read(LIMIT + 1)
        if (len(raw) > LIMIT or len(raw) != package['size']
                or hashlib.sha256(raw).hexdigest() != package['sha256']
                or read_archive(io.BytesIO(raw), identity) != files):
            return None
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            for item in archive.infolist():
                name = item.filename.split('/', 1)[1]
                if (item.compress_type != zipfile.ZIP_DEFLATED
                        or item.external_attr >> 16 & 0o777 != (0o755 if name in executables else 0o644)):
                    return None
        return package
    except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile, RuntimeError, EOFError, zlib.error):
        pass
    return None


# Atomically retain a verified universal archive and its cache metadata
def remember_package(path, key, package):
    value = {'key': key, 'package': package,
             'metadata_sha256': hashlib.sha256(json.dumps(package, sort_keys=True).encode()).hexdigest()}
    fd, temporary = tempfile.mkstemp(prefix='.unified-cache-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream, sort_keys=True)
        os.replace(temporary, path.with_suffix('.cache.json'))
    finally:
        Path(temporary).unlink(missing_ok=True)


# Build or reuse a validated universal archive for the selected host
def build(directory=None, records=None, macos_intel=False, host="manager", *, validated_inventories=None, _verified_inputs=None):
    directory = Path(directory or catalog.ROOT / 'dist')
    records = list(catalog.source_records() if records is None else records)
    verified = _verified_inputs or inputs(directory, records, macos_intel)
    if verified._scope != input_scope(directory, records, macos_intel):
        raise ValueError('Validated inputs do not match requested source scope')
    inventories = verified.inventories()
    if host == 'opendeck':
        print('OpenDeck export is packaging compatibility only; Manager-required capabilities and external dependencies are preserved, not supplied by this archive.', flush=True)
    inventory = {'schema_version': 1, 'product': 'plugins-opendeck' if host == 'opendeck' else 'plugins-unified',
                 'source_fingerprint': inventories['macos']['source_fingerprint'],
                 'support_policy': inventories['macos']['support_policy'], 'packages': []}
    for record in sorted(records, key=lambda p: p['id']):
        if set(catalog.supported_platforms(record)) != set(catalog.PLATFORMS):
            print(f"Unified bundle skipped: {record['id']} does not support all three operating systems")
            continue
        path = path_for(directory, record['id'], host)
        key = cache_key(record, inventories, host)
        files, executables, targets = assemble(directory, record, inventories, host, verified=verified)
        cached = cached_package(path, key, record['id'], files, executables, targets)
        if cached is not None and cached['path'] != path.relative_to(directory.resolve()).as_posix():
            cached = None
        if cached is not None:
            inventory['packages'].append(cached)
            print(f'Cached unified bundle: {path}', flush=True)
            continue
        write_archive(path, record['id'], files, executables, host)
        print(f'Unified bundle: {path}', flush=True)
        inventory['packages'].append({'id': record['id'], 'version': record['version'],
            'path': path.relative_to(directory.resolve()).as_posix(), 'sha256': catalog.digest(path),
            'size': path.stat().st_size, 'files': {n: hashlib.sha256(b).hexdigest() for n, b in files.items()},
            'targets': targets})
        remember_package(path, key, inventory['packages'][-1])
    output = directory / ('optional/catalog-opendeck.json' if host == 'opendeck' else 'optional/catalog-unified.json')
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix('.tmp')
    catalog.write(temporary, inventory)
    temporary.replace(output)
    print(f"Built {len(inventory['packages'])} optional unified bundles: {output}")
    if validated_inventories is not None:
        validated_inventories.update(verified.inventories())
    return inventory


# Build host-specific outputs from one validation snapshot, never shared paths
def build_hosts(directory=None, records=None, macos_intel=False, hosts=('manager', 'opendeck'), *, validated_inventories=None):
    """Build host-specific outputs from one validation snapshot, never shared paths.

    Each assembly rechecks the exact source ZIP bytes against the snapshot. A
    replacement between host builds fails rather than receiving earlier validation.
    """
    directory = Path(directory or catalog.ROOT / 'dist')
    records = list(catalog.source_records() if records is None else records)
    hosts = tuple(hosts)
    if not hosts or len(set(hosts)) != len(hosts) or any(host not in ('manager', 'opendeck') for host in hosts):
        raise ValueError('Expected distinct supported package hosts')
    verified = inputs(directory, records, macos_intel)
    results = {host: build(directory, records, macos_intel, host, _verified_inputs=verified) for host in hosts}
    if validated_inventories is not None:
        validated_inventories.update(verified.inventories())
    return results


# Validate a universal archive against the expected source and platform inputs
def validate(directory=None, records=None, inventory=None, host="manager"):
    directory = Path(directory or catalog.ROOT / 'dist')
    records = list(catalog.source_records() if records is None else records)
    inventory = inventory if inventory is not None else json.loads((directory / ('optional/catalog-opendeck.json' if host == 'opendeck' else 'optional/catalog-unified.json')).read_text())
    inventories = {platform: json.loads((directory / f'catalog-{platform}.json').read_text()) for platform in catalog.PLATFORMS}
    macos_intel = any(p['architectures'] == ['amd64'] for p in inventories['macos']['packages'])
    verified = inputs(directory, records, macos_intel)
    inventories = verified.inventories()
    for platform in inventories.values():
        if any(record.get('source_fingerprint') != platform.get('source_fingerprint') for record in records):
            raise ValueError('Unified source fingerprint changed; rebuild')
    eligible = {p['id']: p for p in records if set(catalog.supported_platforms(p)) == set(catalog.PLATFORMS)}
    if (inventory.get('schema_version') != 1 or inventory.get('product') != ('plugins-opendeck' if host == 'opendeck' else 'plugins-unified')
            or inventory.get('source_fingerprint') != inventories['macos']['source_fingerprint']
            or inventory.get('support_policy') != inventories['macos']['support_policy']
            or len(inventory['packages']) != len(eligible)
            or {p['id'] for p in inventory['packages']} != set(eligible)):
        raise ValueError('Unified inventory differs from source')
    for package in inventory['packages']:
        record = eligible[package['id']]
        files, executables, targets = assemble(directory, record, inventories, host, verified=verified)
        path = path_for(directory, record['id'], host)
        if (package['path'] != path.relative_to(directory.resolve()).as_posix()
                or package['version'] != record['version'] or package['targets'] != targets
                or package['size'] != path.stat().st_size or package['sha256'] != catalog.digest(path)
                or package['files'] != {n: hashlib.sha256(b).hexdigest() for n, b in files.items()}
                or read_archive(path, record['id']) != files):
            raise ValueError('Unified package differs from verified platform inputs')
        with zipfile.ZipFile(path) as archive:
            for entry in archive.infolist():
                name = entry.filename.split('/', 1)[1]
                expected = zipfile.ZIP_DEFLATED
                if entry.compress_type != expected or (entry.external_attr >> 16 & 0o777) != (0o755 if name in executables else 0o644):
                    raise ValueError('Unified executable permissions/compression mismatch')
    return len(inventory['packages'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, default=catalog.ROOT / 'dist')
    parser.add_argument('--macos-intel', action='store_true', default=os.environ.get('PLUGIN_MACOS_INTEL') == '1')
    parser.add_argument('--validate', action='store_true')
    parser.add_argument('--host', choices=('manager', 'opendeck', 'all'), default='manager')
    parser.add_argument('--plugin', default=os.environ.get('PLUGIN') or None)
    args = parser.parse_args()
    records = catalog.source_records()
    if args.plugin:
        records = [record for record in records if record['slug'] == args.plugin]
        if not records:
            parser.error('Unknown plugin selection')
    if args.validate:
        for host in ('manager', 'opendeck') if args.host == 'all' else (args.host,):
            validate(args.directory, records, host=host)
    elif args.host == 'all':
        build_hosts(args.directory, records, macos_intel=args.macos_intel)
    else:
        build(args.directory, records, macos_intel=args.macos_intel, host=args.host)
