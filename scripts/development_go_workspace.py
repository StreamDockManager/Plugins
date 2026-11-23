#!/usr/bin/env python3
"""Bind native development CI to an exact SDK checkout without changing the public pin."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

from sdk_pin import MODULE, release_version, requirement


# Select only the verified local SDK and use native paths on every runner
def create_workspace(root, sdk_checkout, temporary, sdk_commit, expected_os, expected_arch):
    root, sdk_checkout = root.resolve(), sdk_checkout.resolve()
    sdk = sdk_checkout / 'Go'
    env = dict(os.environ, GOWORK='off', GOENV='off')

    # Keep command arguments separate so spaces and Windows drive letters survive
    def read(args, *, cwd=root):
        return subprocess.check_output(args, cwd=cwd, env=env, encoding='utf-8').strip()

    go = json.loads(read(['go', 'env', '-json', 'GOHOSTOS', 'GOHOSTARCH', 'GOOS', 'GOARCH']))
    if (go['GOHOSTOS'] != expected_os or go['GOOS'] != expected_os
            or go['GOHOSTARCH'] != expected_arch or go['GOARCH'] != expected_arch):
        raise ValueError(f'Go host and target must match this native runner: {expected_os}/{expected_arch}; got {go}')
    if (not re.fullmatch(r'[0-9a-f]{40}', sdk_commit)
            or read(['git', 'rev-parse', 'HEAD'], cwd=sdk_checkout) != sdk_commit
            or not (sdk / 'go.mod').is_file()):
        raise ValueError('Expected the exact requested SDK commit and its Go module')
    pin = release_version(requirement(json.loads(read(['go', 'mod', 'edit', '-json'])))['Version'])
    work = Path(tempfile.mkdtemp(prefix='plugin-sdk-', dir=temporary)).resolve()
    read(['go', 'work', 'init', str(root), str(sdk)], cwd=work)
    path = work / 'go.work'
    env['GOWORK'] = str(path)
    read(['go', 'work', 'edit', '-replace', f'{MODULE}@{pin}={sdk.as_posix()}'])
    module = json.loads(read(['go', 'list', '-m', '-json', MODULE]))
    if not module.get('Dir') or Path(module['Dir']).resolve() != sdk:
        raise ValueError('Go did not select the requested development SDK')
    return path


if __name__ == '__main__':
    root = Path(os.environ['GITHUB_WORKSPACE'])
    workspace = create_workspace(
        root, root / '.development-sdk', Path(os.environ['RUNNER_TEMP']),
        os.environ['SDK_COMMIT'], os.environ['EXPECTED_OS'], os.environ['EXPECTED_ARCH'],
    )
    with Path(os.environ['GITHUB_ENV']).open('a', encoding='utf-8', newline='\n') as stream:
        stream.write(f'GOWORK={workspace}\n')
