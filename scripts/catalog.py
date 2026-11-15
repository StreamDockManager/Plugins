#!/usr/bin/env python3
"""Plugin catalog schema 1: source previews, immutable artifacts and channel plans."""
import argparse
import base64
import binascii
from copy import deepcopy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import urlsplit
import zipfile

ROOT = Path(__file__).resolve().parents[1]
HEX = re.compile(r'[0-9a-f]{64}')
REPO = re.compile(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+')
VERSION = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)')
PLATFORMS = {'macos': ['amd64', 'arm64'], 'windows': ['amd64'], 'linux': ['amd64']}

# Load the supported-platform policy used to select release artifacts
def support_policy():
    return validate_policy(json.loads((ROOT / 'internal/catalog/support-policy.json').read_text()))

# Validate platform-policy structure before applying it to source records
def validate_policy(policy):
    if (not isinstance(policy, dict) or policy.get('schema_version') != 1
            or not isinstance(policy.get('retired_ids'), list) or not isinstance(policy.get('platforms'), dict)):
        raise ValueError('Invalid official support policy')
    ids = policy['retired_ids']
    if len(ids) != len(set(ids)) or any(not isinstance(i, str) or not re.fullmatch(r'app\.streamdockmanager\.plugins\.[a-z0-9-]+', i) for i in ids):
        raise ValueError('Invalid retired plugin IDs')
    for identity, platforms in policy['platforms'].items():
        if (identity in ids or not re.fullmatch(r'app\.streamdockmanager\.plugins\.[a-z0-9-]+', identity)
                or not isinstance(platforms, list) or not platforms or len(platforms) != len(set(platforms))
                or not set(platforms) <= PLATFORMS.keys()):
            raise ValueError('Invalid supported plugin platforms')
    return policy

# Return the platforms allowed by the plugin support policy
def supported_platforms(plugin):
    platforms = plugin.get('supported_platforms', list(PLATFORMS))
    if not isinstance(platforms, list) or not platforms or len(platforms) != len(set(platforms)) or not set(platforms) <= PLATFORMS.keys():
        raise ValueError('Invalid plugin supported platforms')
    return platforms

# Check source metadata against the declared platform support policy
def check_source_policy(source, policy):
    validate_policy(policy)
    for plugin in source:
        if plugin['id'] in policy['retired_ids'] or set(supported_platforms(plugin)) != set(policy['platforms'].get(plugin['id'], PLATFORMS)):
            raise ValueError('Source plugin conflicts with saved support policy')

# Filter catalog listings only; immutable package objects are never deleted
def apply_policy(catalog, policy):
    """Filter catalog listings only; immutable package objects are never deleted."""
    validate_policy(policy)
    result = deepcopy(catalog)
    plugins = []
    for plugin in result['plugins']:
        if plugin['id'] in policy['retired_ids']:
            continue
        if plugin['id'] in policy['platforms']:
            platforms = policy['platforms'][plugin['id']]
            plugin['supported_platforms'] = list(platforms)
            plugin['downloads'] = [a for a in plugin['downloads'] if a['os'] in platforms]
            if set(platforms) != set(PLATFORMS):
                plugin.pop('unified_download', None)
                plugin.pop('opendeck_download', None)
            dependencies = []
            for dependency in plugin.get('dependencies', []):
                dependency['platforms'] = [platform for platform in dependency['platforms'] if platform in platforms]
                if dependency['platforms']:
                    dependencies.append(dependency)
            plugin['dependencies'] = dependencies
            if not plugin['downloads'] and not result.get('preview'):
                continue
        plugins.append(plugin)
    result['plugins'] = plugins
    return result

# Catalog schema v1 already carries architectures; variants need no wire change
def artifact_architecture(osname, architectures):
    if not isinstance(architectures, list) or any(not isinstance(a, str) for a in architectures) or len(set(architectures)) != len(architectures):
        raise ValueError('Invalid artifact architectures')
    architectures = sorted(architectures)
    if osname == 'macos' and architectures == ['amd64', 'arm64']:
        return 'universal'
    if osname in PLATFORMS and architectures in [[a] for a in PLATFORMS[osname]]:
        return architectures[0]
    raise ValueError('Unsupported artifact architecture')

# Construct the versioned release filename for the selected package
def artifact_filename(plugin, artifact):
    return f'{plugin["id"]}-{plugin["version"]}-{artifact["os"]}-{artifact_architecture(artifact["os"], artifact["architectures"])}.zip'

# Resolve a package path inside the selected distribution directory
def package_path(directory, package):
    osname = package['os']
    arch = artifact_architecture(osname, package['architectures'])
    folder = 'darwin' if osname == 'macos' else osname
    relative = Path('packages') / folder / arch / (package['id'] + '.zip')
    if package.get('path') != relative.as_posix():
        raise ValueError('Unsupported package inventory path; rebuild packages with the current builder')
    result = Path(directory) / relative
    if not result.resolve().is_relative_to(Path(directory).resolve()):
        raise ValueError('Package escapes inventory directory')
    return result

# Select package variants for the requested plugin and platform
def selected_packages(packages, identity, osname, *, macos_intel=False):
    values = [a for a in packages if a['id'] == identity and a['os'] == osname]
    variants = [artifact_architecture(a['os'], a['architectures']) for a in values]
    if len(variants) != len(set(variants)):
        raise ValueError('Duplicate package variant')
    if osname == 'macos':
        if (set(variants) == {'universal'} or set(variants) == {'amd64', 'arm64'}
                or not macos_intel and set(variants) == {'arm64'}):
            return values
    elif variants == ['amd64']:
        return values
    raise ValueError(f'{identity}: incomplete {osname} package variants')

# Stream file bytes into a SHA-256 integrity digest
def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()

# Serialize the catalog document and create its parent directory
def write(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=4) + '\n')

# Validate the version string used in plugin catalog metadata
def semver(value):
    if not isinstance(value, str) or not VERSION.fullmatch(value):
        raise ValueError(f'Invalid stable version: {value}')
    return tuple(map(int, value.split('.')))

# Check that a catalog artifact path is safe and relative
def safe_path(value):
    return isinstance(value, str) and value and not any(c in value for c in '\\:\0') and not value.startswith('/') and all(p not in ('', '.', '..') for p in value.split('/'))

# Validate an HTTPS URL without embedded credentials
def https_url(value):
    if not isinstance(value,str) or any(c.isspace() or ord(c)<32 or ord(c)==127 for c in value) or '\\' in value:
        return False
    try:
        url=urlsplit(value)
        # Reading port validates its syntax and range; urlsplit alone does not
        _ = url.port
        return (url.scheme=='https' and bool(url.hostname) and url.username is None
                and url.password is None and '#' not in value)
    except (ValueError,UnicodeError):
        return False

# Create the initial catalog envelope for a repository and channel
def empty(repository, channel):
    if (not isinstance(repository, str) or not REPO.fullmatch(repository)
            or any(part in ('.', '..') for part in repository.split('/'))
            or channel not in ('stable', 'development')):
        raise ValueError('Invalid repository/channel')
    return {'schema_version': 1, 'product': 'plugins', 'repository': repository, 'channel': channel, 'plugins': []}

# Validate the public catalog base URL before constructing download links
def public_base(value):
    if not https_url(value) or urlsplit(value).query or '%' in urlsplit(value).path:
        raise ValueError('Public catalog URL requires HTTPS without credentials, query, fragment, or encoded paths')
    path = urlsplit(value).path
    if any(p in ('.', '..') for p in path.split('/')) or '//' in path:
        raise ValueError('Invalid public catalog path')
    return value.rstrip('/')

# Only the selected repository's release API or exact catalog asset
def github_catalog_source(value, repository, channel):
    """Only the selected repository's release API or exact catalog asset."""
    if value == f'https://api.github.com/repos/{repository}/releases':
        return True
    if not isinstance(value, str):
        return False
    return re.fullmatch(
        r'https://github\.com/' + re.escape(repository) + r'/releases/download/'
        + r'plugins-' + re.escape(channel) + r'-[A-Za-z0-9._-]+/catalog\.json', value
    ) is not None


# Check whether the download URL matches the catalog source policy
def artifact_url_valid(value, repository, channel, plugin, artifact, catalog_url=None):
    if not https_url(value) or (catalog_url is not None and not https_url(catalog_url)):
        return False
    github_url = f'https://github.com/{repository}/releases/download/{artifact.get("release_tag")}/{artifact["filename"]}'
    if catalog_url and github_catalog_source(catalog_url, repository, channel):
        return value == github_url
    # GitHub hosts are never treated as arbitrary CDN directories. In particular,
    # a foreign release cannot pass the hosted suffix-only offline validation
    github_hosts = ('github.com', 'api.github.com', 'objects.githubusercontent.com', 'release-assets.githubusercontent.com')
    if urlsplit(value).hostname in github_hosts:
        return catalog_url is None and value == github_url
    if catalog_url and urlsplit(catalog_url).hostname in github_hosts:
        return False
    suffix = f'packages/{channel}/{plugin["id"]}/{plugin["version"]}/{artifact["sha256"]}/{artifact["filename"]}'
    if catalog_url:
        return value == public_base(catalog_url).rsplit('/', 1)[0] + '/' + suffix
    try:
        public_base(value)
        return urlsplit(value).path.endswith('/' + suffix)
    except ValueError:
        return False

# Validate an explicit cross-platform host artifact, never an OS variant
def validate_unified_download(artifact, plugin, repository, channel, catalog_url=None, *, host="manager"):
    """Validate an explicit cross-platform host artifact, never an OS variant."""
    suffix = "opendeck-universal" if host == "opendeck" else "unified"
    if not isinstance(artifact, dict) or 'os' in artifact or 'architectures' in artifact:
        raise ValueError('Unified download must use explicit targets')
    targets = artifact.get('targets')
    if (not isinstance(targets, list) or len(targets) != 3
            or any(not isinstance(t, dict) or not isinstance(t.get('os'), str) for t in targets)
            or {t['os'] for t in targets} != set(PLATFORMS)):
        raise ValueError('Unified download requires all three platforms')
    for target in targets:
        if target['os'] not in supported_platforms(plugin):
            raise ValueError('Unified download contradicts supported platforms')
        artifact_architecture(target['os'], target.get('architectures'))
    if artifact.get('filename') != f'{plugin["id"]}-{plugin["version"]}-{suffix}.zip':
        raise ValueError('Invalid unified artifact filename')
    if (not isinstance(artifact.get('sha256'), str) or not HEX.fullmatch(artifact['sha256'])
            or not artifact_url_valid(artifact.get('url'), repository, channel, plugin, artifact, catalog_url)
            or not isinstance(artifact.get('release_tag'), str)
            or not re.fullmatch(r'plugins-' + channel + r'-[A-Za-z0-9._-]+', artifact['release_tag'])
            or not isinstance(artifact.get('source_commit'), str)
            or not re.fullmatch(r'(?:[a-f0-9]{40}|[a-f0-9]{64})', artifact['source_commit'])
            or type(artifact.get('size')) is not int or not 0 < artifact['size'] <= 256 * 1024 * 1024):
        raise ValueError('Invalid unified artifact URL/hash/size/source')
    files = artifact.get('files')
    if (not isinstance(files, dict) or not files or len(files) > 4096 or 'manifest.json' not in files
            or any(not safe_path(n) or not isinstance(h, str) or not HEX.fullmatch(h) for n, h in files.items())):
        raise ValueError('Invalid unified complete file inventory')
    if host == 'opendeck':
        compatibility = artifact.get('compatibility')
        if (not isinstance(compatibility, dict) or compatibility.get('status') != 'unverified'
                or not isinstance(compatibility.get('reason'), str) or not compatibility['reason'].strip()
                or 'evidence' in compatibility or 'vsd_craft_compatible' in artifact):
            raise ValueError('OpenDeck packaging is not native runtime qualification')
    return artifact

# Validate catalog structure, source identity, and download metadata
def validate(catalog, production=True, catalog_url=None):
    empty(catalog.get('repository', ''), catalog.get('channel'))
    if catalog.get('schema_version') != 1 or catalog.get('product') != 'plugins' or (production and catalog.get('preview')) or not isinstance(catalog.get('plugins'), list) or len(catalog['plugins']) > 256:
        raise ValueError('Invalid plugin catalog schema 1')
    seen = set()
    for p in catalog['plugins']:
        if not re.fullmatch(r'[a-z0-9]+(?:[.-][a-z0-9]+)*', p['id']) or p['id'] in seen:
            raise ValueError('Invalid/duplicate plugin ID')
        seen.add(p['id'])
        semver(p['version']); semver(p['minimum_manager'])
        supported_platforms(p)
        for field in ('name', 'description', 'author', 'license', 'documentation', 'icon'):
            if not isinstance(p.get(field), str) or not p[field]:
                raise ValueError(f'{p["id"]}: missing {field}')
        if not p['icon'].startswith('data:image/svg+xml;base64,') or len(p['icon']) > 256 * 1024:
            raise ValueError('Invalid catalog icon')
        if not https_url(p['documentation']):
            raise ValueError(f'{p["id"]}: Documentation requires an absolute HTTPS URL without credentials or fragments')
        if p.get('capabilities', {}).get('id') != p['id'] or p['capabilities'].get('plugin_version') != p['version'] or p['capabilities'].get('version') != 1 or p['capabilities'].get('minimum_manager') != p['minimum_manager'] or p['capabilities'].get('required_capabilities', []) != p['required_capabilities'] or set(p['capabilities'].get('actions', {})) != {p['id'] + '.' + a for a in p['actions']}:
            raise ValueError('Descriptor identity mismatch')
        for dep in p['dependencies']:
            if not dep['name'] or not isinstance(dep['required'], bool) or not https_url(dep['documentation']) or not set(dep['platforms']) <= PLATFORMS.keys() or not set(dep['actions']) <= set(p['actions']):
                raise ValueError(f'{p["id"]}: Invalid dependency applicability: {dep["name"]}')
        if production and not p['downloads']:
            raise ValueError('Published plugin has no artifacts')
        if 'unified_download' in p:
            validate_unified_download(p['unified_download'], p, catalog['repository'], catalog['channel'], catalog_url)
        if 'opendeck_download' in p:
            validate_unified_download(p['opendeck_download'], p, catalog['repository'], catalog['channel'], catalog_url, host='opendeck')
        platforms = set()
        for a in p['downloads']:
            osname = a['os']
            if osname not in supported_platforms(p):
                raise ValueError('Download contradicts supported platforms')
            arch = artifact_architecture(osname, a.get('architectures'))
            a['architectures'] = sorted(a['architectures'])
            if (osname, arch) in platforms:
                raise ValueError('Duplicate/unsupported artifact platform')
            platforms.add((osname, arch))
            filename = f'{p["id"]}-{p["version"]}-{osname}-{arch}.zip'
            if a['filename'] != filename or not re.fullmatch(r'plugins-(stable|development)-[A-Za-z0-9._-]+', a['release_tag']) or not a['release_tag'].startswith('plugins-' + catalog['channel'] + '-'):
                raise ValueError('Invalid immutable artifact identity')
            if not artifact_url_valid(a['url'], catalog['repository'], catalog['channel'], p, a, catalog_url) or not HEX.fullmatch(a['sha256']) or not re.fullmatch('[a-f0-9]{40,64}', a['source_commit']) or not isinstance(a['size'], int) or isinstance(a['size'], bool) or not 0 < a['size'] <= 256 * 1024 * 1024:
                raise ValueError('Invalid artifact URL/hash/size/source')
            if not a['files'] or len(a['files']) > 4096 or 'manifest.json' not in a['files'] or any(not safe_path(n) or not HEX.fullmatch(h) for n, h in a['files'].items()):
                raise ValueError('Invalid complete file inventory')
            c = a['compatibility']
            if c['status'] not in ('verified', 'incompatible', 'unverified') or not c['reason'] or not isinstance(a['vsd_craft_compatible'], bool):
                raise ValueError('Invalid compatibility record')
            if c['status'] == 'verified' or 'evidence' in c:
                evidence = c.get('evidence', {})
                if not isinstance(evidence, dict) or evidence.get('plugin_version') != p['version'] or evidence.get('os') != osname or evidence.get('sha256') != a['sha256'] or not https_url(evidence.get('url')):
                    raise ValueError('Qualification evidence does not match artifact')
            if a['vsd_craft_compatible'] != (c['status'] == 'verified'):
                raise ValueError('Compatibility requires verified evidence')
    return catalog

# Read the complete plugin definitions from the source catalog command
def source_records(env=None):
    from go_modules import environment
    env = environment(os.environ if env is None else env, cwd=ROOT)
    env.setdefault('GOCACHE', str(ROOT / '.cache/go-build'))
    definitions = json.loads(subprocess.check_output(['go', 'run', './tools/pluginbuild', 'catalog'], cwd=ROOT, env=env, text=True))
    metadata = json.loads((ROOT / 'catalog/metadata.json').read_text())
    if set(metadata) != {d['slug'] for d in definitions}:
        raise ValueError('Metadata must cover exactly the source plugins')
    return [dict(d, **metadata[d['slug']], downloads=[]) for d in definitions]


# Select the requested release channel from the available catalog inputs
def select(source, baseline, commit, run_id, policy=None):
    policy = deepcopy(support_policy() if policy is None else validate_policy(policy))
    check_source_policy(source, policy)
    validate(baseline)
    source_catalog = empty(baseline['repository'], baseline['channel'])
    source_catalog['plugins'] = source
    validate(source_catalog, production=False)
    if not re.fullmatch('[a-f0-9]{40,64}', commit) or not re.fullmatch('[A-Za-z0-9._-]+', str(run_id)):
        raise ValueError('Invalid release commit/run identity')
    previous = {p['id']: p for p in baseline['plugins']}
    selected, skipped = [], []
    for p in source:
        old = previous.get(p['id'])
        if old and semver(p['version']) < semver(old['version']):
            raise ValueError(f'{p["id"]}: version decreased')
        (skipped if old and semver(p['version']) == semver(old['version']) else selected).append(p['id'])
    plan = {'schema_version': 1, 'repository': baseline['repository'], 'channel': baseline['channel'], 'commit': commit,
            'tag': f'plugins-{baseline["channel"]}-{run_id}-{commit[:12]}' if selected else '', 'selected': selected, 'skipped': skipped, 'source': source, 'baseline': baseline, 'support_policy': policy}
    plan['sha256'] = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    return plan

# Check the proposed release artifacts against source and package metadata
def verify_plan(plan):
    value = dict(plan); claimed = value.pop('sha256')
    if hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest() != claimed:
        raise ValueError('Release plan integrity failure')
    baseline = plan['baseline']
    prefix = f'plugins-{baseline["channel"]}-'
    suffix = '-' + plan['commit'][:12]
    if plan['tag']:
        if not plan['tag'].startswith(prefix) or not plan['tag'].endswith(suffix):
            raise ValueError('Release plan tag differs from channel/commit')
        run_id = plan['tag'][len(prefix):-len(suffix)]
    else:
        run_id = 'no-change'
    expected = select(plan['source'], baseline, plan['commit'], run_id, plan['support_policy'])
    if expected != plan:
        raise ValueError('Release plan metadata or selection disagrees')

# First-party policy only; vendor imports may use any supported ZIP method
def validate_compression(archive, prefix, executable):
    """First-party policy only; vendor imports may use any supported ZIP method."""
    for entry in archive.infolist():
        expected = zipfile.ZIP_DEFLATED
        if entry.compress_type != expected:
            raise ValueError(f'{entry.filename}: compression policy mismatch')


# Inspect the executable without running it, using Manager's SDMCAP1 bounds
def validate_compiled_capabilities(archive, prefix, executable, expected):
    """Inspect the executable without running it, using Manager's SDMCAP1 bounds."""
    if not safe_path(executable):
        raise ValueError('Unsafe executable capability inspection path')
    try:
        entry = archive.getinfo(prefix + executable)
    except KeyError as error:
        raise ValueError('Capability executable is missing') from error
    executable_limit = 256 * 1024 * 1024
    descriptor_limit = 64 * 1024
    if entry.file_size > executable_limit:
        raise ValueError('Executable exceeds descriptor inspection limit')
    with archive.open(entry) as stream:
        binary = stream.read(executable_limit + 1)
    if len(binary) > executable_limit:
        raise ValueError('Executable exceeds descriptor inspection limit')

    # Reject duplicate JSON object keys while parsing package metadata
    def unique_object(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError('Duplicate capability descriptor field')
            value[key] = item
        return value

    # Reject nonfinite JSON constants instead of accepting ambiguous metadata
    def invalid_constant(value):
        raise ValueError('Invalid capability descriptor number')

    # Serialize a normalized value for deterministic comparison
    def canonical(value):
        # Python's ordinary equality considers True == 1 and 1.0 == 1. Rust's
        # typed descriptor does not, so preserve JSON types when comparing
        return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False)

    marker, suffix = b'SDMCAP1:', b':ENDSDMCAP1'
    offset, descriptor = 0, None
    while True:
        offset = binary.find(marker, offset)
        if offset < 0:
            break
        offset += len(marker)
        header = binary[offset:offset + 9]
        # Go retains the bare prefix constant alongside the actual frame
        if not re.fullmatch(rb'[0-9A-Fa-f]{8}:', header):
            continue
        length = int(header[:8], 16)
        if length > descriptor_limit * 2:
            raise ValueError('Capability descriptor exceeds limit')
        start = offset + 9
        end = start + length
        if binary[end:end + len(suffix)] != suffix:
            raise ValueError('Truncated capability descriptor')
        encoded = binary[start:end]
        try:
            payload = base64.b64decode(encoded, validate=True)
        except binascii.Error as error:
            raise ValueError('Invalid capability descriptor encoding') from error
        if base64.b64encode(payload) != encoded:
            raise ValueError('Invalid capability descriptor encoding')
        if len(payload) > descriptor_limit:
            raise ValueError('Capability descriptor exceeds limit')
        try:
            value = json.loads(payload.decode('utf-8'), object_pairs_hook=unique_object,
                               parse_constant=invalid_constant)
        except (UnicodeError, json.JSONDecodeError, RecursionError) as error:
            raise ValueError('Invalid capability descriptor JSON') from error
        if not isinstance(value, dict) or type(value.get('version')) is not int or value['version'] != 1 or any(type(value.get(key)) is not int or not 0 <= value[key] <= 1 for key in ('theme_protocol', 'settings_protocol', 'input_protocol')):
            raise ValueError('Unsupported compiled capability descriptor protocol')
        value = canonical(value)
        if descriptor is not None and descriptor != value:
            raise ValueError('Conflicting executable capability descriptors')
        descriptor = value
    if descriptor is None:
        raise ValueError('Executable has no compiled capability descriptor')
    if descriptor != canonical(expected):
        raise ValueError('Executable capabilities differ from source descriptor')
    return binary


# Metadata-only catalog updates preserve every immutable artifact field
def annotate(catalog_value, metadata):
    """Metadata-only catalog updates preserve every immutable artifact field."""
    result = deepcopy(catalog_value)
    for plugin in result['plugins']:
        values = metadata.get(plugin['slug'])
        if values is None: continue
        for field in ('description', 'author', 'license', 'tags', 'documentation', 'dependencies'):
            plugin[field] = deepcopy(values[field])
        platforms = values.get('vsd_compatibility', {}).get('platforms', {})
        for osname, record in platforms.items():
            candidates = [a for a in plugin['downloads'] if a['os'] == osname]
            if candidates and 'evidence' in record and not any(a['sha256'] == record['evidence'].get('sha256') for a in candidates):
                raise ValueError('Qualification evidence does not match any platform artifact')
        for artifact in plugin['downloads']:
            record = platforms.get(artifact['os'])
            if record is None: continue
            if 'evidence' in record and record['evidence'].get('sha256') != artifact['sha256']: continue
            artifact['compatibility'] = deepcopy(record)
            artifact['vsd_craft_compatible'] = record['status'] == 'verified'
    return validate(result)


# Run catalog selection, validation, or annotation from command-line options
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['validate', 'source'])
    parser.add_argument('--file', type=Path)
    parser.add_argument('--repository', default=os.environ.get('RELEASE_REPOSITORY', 'StreamDockManager/Plugins'))
    parser.add_argument('--channel', default='stable')
    args = parser.parse_args()
    if args.command == 'validate': validate(json.loads(args.file.read_text()))
    elif args.command == 'source': print(json.dumps(source_records(), indent=4))

if __name__ == '__main__': main()
