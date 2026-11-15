"""Public SDK resolution by default; explicit private mirrors remain process-local."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import urlsplit

SDK_REPOSITORY = 'github.com/StreamDockManager/Plugin-SDK'
SDK_HTTPS = 'https://' + SDK_REPOSITORY
SDK_SSH = 'git@github.com:StreamDockManager/Plugin-SDK.git'
PRIVATE_KEYS = ('GOPRIVATE', 'GONOPROXY', 'GONOSUMDB')


# Validate an explicit credential-free SDK source URL
def repository_url(value):
    if not isinstance(value, str) or not value or any(ord(c) < 33 or ord(c) == 127 for c in value):
        raise ValueError('SDK repository URL must be a credential-free Git URL')
    if re.fullmatch(r'[\w.-]+@[\w.-]+:[\w./-]+', value):
        return value
    parsed = urlsplit(value)
    if (parsed.scheme not in ('https', 'ssh', 'file') or parsed.password is not None
            or parsed.query or parsed.fragment or not parsed.path
            or parsed.scheme != 'file' and not parsed.hostname
            or parsed.scheme == 'https' and parsed.username is not None):
        raise ValueError('Use an SSH URL or credential-free HTTPS URL; authenticate through Git, not workspace configuration')
    return value


# Preserve public Go resolution and apply only explicitly requested private overrides
def environment(env=None, *, sdk_url=None, cwd=None):
    result = dict(os.environ if env is None else env)
    target = repository_url(sdk_url if sdk_url is not None else result.get('STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL', SDK_HTTPS))
    # Public SDK uses normal Go proxy/checksum policy. Preserve all user settings
    # Only an explicit alternative source opts this module into private resolution
    if target not in (SDK_HTTPS, SDK_HTTPS + '.git'):
        saved = {}
        if result.get('GOENV') != 'off' and any(key not in result for key in PRIVATE_KEYS):
            saved = json.loads(subprocess.check_output(
                ['go', 'env', '-json', *PRIVATE_KEYS], cwd=cwd, env=result, text=True))
        for key in PRIVATE_KEYS:
            patterns = result.get(key, saved.get(key, '')).split(',')
            result[key] = ','.join(dict.fromkeys([p.strip() for p in patterns if p.strip()] + [SDK_REPOSITORY]))
    result['STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL'] = target
    result.setdefault('GIT_TERMINAL_PROMPT', '0')
    count = int(result.get('GIT_CONFIG_COUNT') or '0')
    if not 0 <= count <= 1024:
        raise ValueError('Invalid GIT_CONFIG_COUNT')
    entries = []
    for index in range(count):
        key, value = f'GIT_CONFIG_KEY_{index}', f'GIT_CONFIG_VALUE_{index}'
        if key not in result or value not in result:
            raise ValueError('Incomplete process-local Git configuration')
        entries.append((result[key], result[value]))
    if target not in (SDK_HTTPS, SDK_HTTPS + '.git'):
        for source in (SDK_HTTPS + '.git', SDK_HTTPS):
            rewrite = (f'url.{target}.insteadOf', source)
            if rewrite not in entries:
                if count >= 1024:
                    raise ValueError('Too many process-local Git settings')
                result[f'GIT_CONFIG_KEY_{count}'], result[f'GIT_CONFIG_VALUE_{count}'] = rewrite
                count += 1
        result['GIT_CONFIG_COUNT'] = str(count)
    return result


# Export only changed SDK resolution settings to the CI environment
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--github-env', type=Path, required=True, help='GitHub Actions environment file')
    args = parser.parse_args()
    before = dict(os.environ)
    after = environment(before)
    # Emit only newly configured values, never inherited credential headers
    with args.github_env.open('a', encoding='utf-8') as output:
        for key, value in after.items():
            if before.get(key) != value:
                if '\n' in value or '\r' in value:
                    raise ValueError('Private module settings must be single-line values')
                output.write(f'{key}={value}\n')
    print('Configured SDK module access; public defaults preserve Go proxy and checksum verification')


if __name__ == '__main__':
    main()
