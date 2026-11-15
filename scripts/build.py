#!/usr/bin/env python3
"""Standalone Go plugin builds with public repository/catalog defaults."""
import argparse
import os
from pathlib import Path
import re
import subprocess
import sys
import tomllib
from urllib.parse import urlsplit
import importlib.util

spec = importlib.util.spec_from_file_location('plugins_go_modules', Path(__file__).with_name('go_modules.py'))
go_modules = importlib.util.module_from_spec(spec)
spec.loader.exec_module(go_modules)

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ['help', 'deps', 'build', 'package', 'unified', 'cross', 'check', 'test', 'catalog', 'assets']


# Resolve standalone build configuration while preserving inherited tool settings
def environment():
    env = os.environ.copy()
    path = Path(env.get('WORKSPACE_CONFIG') or ROOT / 'workspace.toml')
    with path.open('rb') as source:
        config = tomllib.load(source)
    settings = config.get('plugins', {})
    if not isinstance(settings, dict):
        raise ValueError('workspace.toml plugins settings must be a TOML table')
    repository = env.get('RELEASE_REPOSITORY', settings.get('release_repository', ''))
    if not isinstance(repository, str) or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('plugins.release_repository must use GitHub owner/repository syntax')
    url = env.get('STREAM_DOCK_MANAGER_PLUGINS_CATALOG_URL', settings.get('catalog_url', ''))
    if not isinstance(url, str):
        raise ValueError('plugins.catalog_url must be a string')
    if url:
        parsed = urlsplit(url)
        if (parsed.scheme != 'https' or not parsed.hostname or parsed.username is not None
                or parsed.password is not None or parsed.query or parsed.fragment
                or any(c.isspace() or ord(c) < 32 or ord(c) == 127 for c in url)
                or '\\' in url or '%' in url or parsed.path.endswith('/')):
            raise ValueError('plugins.catalog_url must be a complete public HTTPS catalog URL')
        _ = parsed.port
    env['RELEASE_REPOSITORY'] = repository
    env['STREAM_DOCK_MANAGER_PLUGINS_CATALOG_URL'] = url
    if 'sdk_repository_url' in settings:
        env.setdefault('STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL', settings['sdk_repository_url'])
    # The helper itself always runs natively; cross selects its own binary targets
    env.pop('GOOS', None)
    env.pop('GOARCH', None)
    jobs = int(env.get('WORKSPACE_JOBS') or min(4, os.cpu_count() or 1))
    if not 1 <= jobs <= 128:
        raise ValueError('WORKSPACE_JOBS must be between 1 and 128')
    env.setdefault('GOMAXPROCS', str(jobs))
    return env, jobs


# Construct the selected standalone build command and target arguments
def command(action, env, jobs, host=None):
    if host in ('opendeck', 'all') and action != 'unified':
        raise ValueError('OpenDeck supports universal final bundles only: run cross, then unified --host opendeck')
    if action == 'deps':
        return ['go', 'mod', 'download']
    if action == 'test':
        return ['go', 'test', './...']
    if action == 'unified':
        return [sys.executable, str(ROOT / 'scripts/unified_packages.py'), '--host', host or 'all']
    args = ['go', 'run', './tools/pluginbuild', action]
    if action in ['build', 'package', 'cross']:
        args += ['--jobs', str(jobs)]
    if env.get('PLUGIN'):
        args += ['--plugin', env['PLUGIN']]
    if action == 'package':
        host = subprocess.check_output(['go', 'env', 'GOHOSTOS', 'GOHOSTARCH'], cwd=ROOT, env=env, text=True).split()
        if len(host) != 2:
            raise ValueError('Could not determine the native Go package target')
        args += ['--os', host[0], '--arch', host[1]]
    return args


# Dispatch the requested standalone plugin build target
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', nargs='?', choices=TARGETS, default='help')
    parser.add_argument('--host', choices=('manager', 'opendeck', 'all'), default=os.environ.get('PLUGIN_HOST') or None)
    options = parser.parse_args()
    action = options.target
    if action == 'help':
        print('Standalone Stream Dock Manager plugins (GNU Make, Python 3.11+, Go 1.27.4).\n'
              'deps | build (native executables) | package (native installable ZIPs)\n'
              'cross (Windows/Linux amd64, macOS arm64) | unified (combine verified platform ZIPs)\n'
              'unified exports Manager and OpenDeck all-OS ZIPs; --host selects one.\n'
              'Native build/package/cross targets remain Manager/VSD only.\n'
              'check | test | catalog | assets\n'
              'PLUGIN_MACOS_INTEL=1 also cross-compiles optional Intel macOS binaries.\n'
              'PLUGIN=<slug> selects one plugin; WORKSPACE_JOBS bounds parallel builds.\n'
              'Defaults: this repository/workspace.toml; WORKSPACE_CONFIG selects another file.\n'
              'RELEASE_REPOSITORY and STREAM_DOCK_MANAGER_PLUGINS_CATALOG_URL override public defaults.\n'
              'Install the resulting ZIPs through Manager: Plugins > Install plugin.')
        return
    env, jobs = environment()
    env = go_modules.environment(env, cwd=ROOT)
    subprocess.run(command(action, env, jobs, options.host), cwd=ROOT, env=env, check=True)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
