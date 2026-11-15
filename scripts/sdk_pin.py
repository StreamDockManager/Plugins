#!/usr/bin/env python3
"""Check or update the exact published SDK Go release without changing other dependencies."""
import argparse
from copy import deepcopy
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

import go_modules

ROOT = Path(__file__).resolve().parents[1]
MODULE = 'github.com/StreamDockManager/Plugin-SDK/Go'
RELEASE = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?')


# Require a release version rather than a moving ref, pseudo-version or incompatible module major
def release_version(value):
    prerelease = value.split('-', 1)[1] if '-' in value else None
    if (not RELEASE.fullmatch(value) or value.split('.')[0] not in ('v0', 'v1')
            or re.search(r'[.-][0-9]{14}-[0-9a-f]{12}$', value)
            or prerelease is not None and any(
                not part or part.isdigit() and len(part) > 1 and part.startswith('0')
                for part in prerelease.split('.'))):
        raise ValueError('Use an exact SDK release vX.Y.Z (repository tag Go/vX.Y.Z), not a ref or pseudo-version')
    return value


# Run Go using the staged module and decode its machine-readable response
def go(arguments, module_file, env, root):
    return json.loads(subprocess.check_output(
        ['go', 'mod', arguments[0], '-modfile=' + str(module_file), *arguments[1:]],
        cwd=root, env=env, text=True))



# Locate the one SDK requirement and reject local replacement authority
def requirement(document):
    if any(item['Old']['Path'] == MODULE for item in document.get('Replace') or []):
        raise ValueError('Remove the SDK replace directive before selecting a published release')
    matches = [item for item in document.get('Require') or [] if item['Path'] == MODULE]
    if len(matches) != 1:
        raise ValueError('Expected exactly one SDK requirement')
    return matches[0]



# Stage resolution before replacing either tracked file, restoring originals on a failed write
def pin(action, version=None, root=ROOT):
    root = Path(root)
    paths = [root / 'go.mod', root / 'go.sum']
    before = [path.read_bytes() for path in paths]
    env = go_modules.environment(cwd=root)
    env['GOWORK'] = 'off'
    with tempfile.TemporaryDirectory(prefix='sdk-release-pin-') as temporary:
        staged = [Path(temporary) / path.name for path in paths]
        for path, data in zip(staged, before):
            path.write_bytes(data)
        original = go(['edit', '-json'], staged[0], env, root)
        current = requirement(original)['Version']
        selected = release_version(version or current)
        if action == 'check' and current != selected:
            raise ValueError(f'SDK is pinned to {current}, not {selected}')
        if action == 'update':
            subprocess.run(['go', 'mod', 'edit', '-modfile=' + str(staged[0]),
                            '-require=' + MODULE + '@' + selected], cwd=root, env=env, check=True)
        resolved = go(['download', '-json', MODULE + '@' + selected], staged[0], env, root)
        if (resolved.get('Error') or resolved.get('Path') != MODULE
                or resolved.get('Version') != selected
                or not resolved.get('Sum', '').startswith('h1:')
                or not resolved.get('GoModSum', '').startswith('h1:')):
            raise ValueError('Published SDK resolution did not provide the exact version and checksums')
        origin = resolved.get('Origin') or {}
        if origin.get('Ref') and origin['Ref'] != 'refs/tags/Go/' + selected:
            raise ValueError('Resolved SDK origin is not the expected Go release tag')
        final = go(['edit', '-json'], staged[0], env, root)
        expected = deepcopy(original)
        requirement(expected)['Version'] = selected
        if final != expected:
            raise ValueError('SDK update would change unrelated module configuration')
        old_sums = set(before[1].splitlines())
        new_sums = set(staged[1].read_bytes().splitlines())
        permitted = {f'{MODULE} {selected} {resolved["Sum"]}'.encode(),
                     f'{MODULE} {selected}/go.mod {resolved["GoModSum"]}'.encode()}
        if not old_sums <= new_sums or not new_sums - old_sums <= permitted or not permitted <= new_sums:
            raise ValueError('SDK resolution changed unrelated checksums or omitted release checksums')
        if [path.read_bytes() for path in paths] != before:
            raise ValueError('Module files changed during SDK resolution; retry after reviewing edits')
        if action == 'check':
            if not permitted <= old_sums:
                raise ValueError('SDK checksums are missing; run update for this release first')
            return selected
        # Same-directory replacements avoid cross-filesystem renames; rollback covers ordinary failures
        pending = []
        try:
            for path, source in zip(paths, staged):
                descriptor, name = tempfile.mkstemp(prefix='.' + path.name + '.', dir=root)
                pending.append(Path(name))
                with os.fdopen(descriptor, 'wb') as stream:
                    stream.write(source.read_bytes())
                os.chmod(name, path.stat().st_mode & 0o777)
            replaced = 0
            try:
                for source, target in zip(pending, paths):
                    os.replace(source, target)
                    replaced += 1
            except OSError:
                for path, data in zip(paths[:replaced], before[:replaced]):
                    path.write_bytes(data)
                raise
        finally:
            for path in pending:
                path.unlink(missing_ok=True)
    return selected


# Expose separate read-only verification and explicit release update commands
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    update = sub.add_parser('update')
    update.add_argument('version', type=release_version)
    check = sub.add_parser('check')
    check.add_argument('--version', type=release_version)
    args = parser.parse_args()
    try:
        selected = pin(args.action, args.version)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f'SDK pin check failed: {error}\n')
    print('SDK release verified: ' + selected)


if __name__ == '__main__':
    main()
