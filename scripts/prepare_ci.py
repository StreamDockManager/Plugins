"""One CI test/build pass, using public pinned SDK dependencies."""
import os
import subprocess
import go_modules

# Keep all validation ahead of the single package build and seal only its completed output
if __name__ == '__main__':
    env = go_modules.environment()
    files = subprocess.check_output(['git', 'ls-files', '-z', '--', '*.go']).decode().split('\0')
    unformatted = subprocess.check_output(['gofmt', '-l', *[f for f in files if f]], text=True)
    if unformatted.strip():
        raise SystemExit('Go formatting required: ' + unformatted)
    for command in [
        ['python3', 'scripts/sdk_pin.py', 'check'],
        ['go', 'mod', 'download'], ['go', 'mod', 'verify'],
        ['go', 'run', 'golang.org/x/vuln/cmd/govulncheck@latest', './...'],
        ['go', 'vet', './...'], ['go', 'test', '-vet=off', './...'],
        ['python3', '-m', 'unittest', 'discover', '-s', 'scripts/tests', '-p', 'test_*.py'],
        ['python3', 'scripts/release.py', 'version'],
    ]:
        subprocess.run(command, env=env, check=True)
    # Derive the tag from this run so publish selects the exact prepared bytes without rebuilding
    command = ['python3', 'scripts/github_release.py', 'prepare', '--build',
               '--tag', f'plugins-{env["RELEASE_CHANNEL"]}-{env["GITHUB_RUN_ID"]}',
               '--commit', env['GITHUB_SHA'], '--channel', env['RELEASE_CHANNEL'],
               '--output', 'dist/github-release-bundle']
    if env.get('MACOS_INTEL') == 'true': command.append('--macos-intel')
    subprocess.run(command, env=env, check=True)
    subprocess.run(['python3', 'scripts/prepared_release.py', 'seal', '--directory',
                    'dist/github-release-bundle'], env=env, check=True)
