"""Bind a release bundle to a successful main-branch Prepare Release run."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile

WORKFLOW = '.github/workflows/prepare-release.yml'
NAME = 'prepared-plugin-release'
MAX_BYTES = 2 * 1024**3


# Stream file bytes into a SHA-256 integrity digest
def digest(path):
    with Path(path).open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


# Require a successful same-repository main-branch preparation workflow run
def validate_run(run, repository, run_id, workflow_id):
    if (type(run_id) is not int or run_id < 1
            or type(run.get('run_attempt')) is not int or run['run_attempt'] < 1
            or run.get('id') != run_id or run.get('repository', {}).get('full_name') != repository
            or run.get('head_repository', {}).get('full_name') != repository
            or run.get('workflow_id') != workflow_id or run.get('path') != WORKFLOW
            or run.get('event') != 'workflow_dispatch' or run.get('head_branch') != 'main'
            or run.get('status') != 'completed' or run.get('conclusion') != 'success'
            or not re.fullmatch('[a-f0-9]{40}', run.get('head_sha', ''))):
        raise ValueError('Not a successful same-repository main Prepare Release run')


# Record source, run, version, and manifest identity for the prepared bundle
def seal(directory):
    directory = Path(directory)
    manifest = json.loads((directory / 'release.json').read_text())
    doc = json.loads((directory / 'catalog.json').read_text())
    provenance = dict(schema_version=1, repository=os.environ['GITHUB_REPOSITORY'],
                      commit=os.environ['GITHUB_SHA'], run_id=int(os.environ['GITHUB_RUN_ID']),
                      run_attempt=int(os.environ['GITHUB_RUN_ATTEMPT']), workflow=WORKFLOW,
                      versions={p['id']: p['version'] for p in doc['plugins']},
                      manifest_sha256=digest(directory / 'release.json'))
    if manifest['commit'] != provenance['commit'] or manifest['repository'] != provenance['repository']:
        raise ValueError('Prepared source identity mismatch')
    (directory / 'provenance.json').write_text(json.dumps(provenance, sort_keys=True) + '\n')


# Verify the sealed preparation identity and every staged asset checksum
def verify_bundle(directory, run, repository):
    directory = Path(directory)
    p = json.loads((directory / 'provenance.json').read_text())
    m = json.loads((directory / 'release.json').read_text())
    c = json.loads((directory / 'catalog.json').read_text())
    if (p.get('schema_version') != 1 or p.get('repository') != repository
            or p.get('commit') != run['head_sha'] or p.get('run_id') != run['id']
            or p.get('run_attempt') != run['run_attempt'] or p.get('workflow') != WORKFLOW
            or p.get('manifest_sha256') != digest(directory / 'release.json')
            or p.get('versions') != {v['id']: v['version'] for v in c['plugins']}
            or m.get('repository') != repository or m.get('commit') != run['head_sha']
            or m.get('channel') not in ('stable', 'development')
            or m.get('tag') != f'plugins-{m.get("channel")}-{run["id"]}'):
        raise ValueError('Prepared bundle provenance/version mismatch')
    expected = set(m['assets']) | {'release.json', 'provenance.json'}
    if {x.name for x in directory.iterdir()} != expected:
        raise ValueError('Unexpected prepared bundle files')
    for name, value in m['assets'].items():
        path = directory / name
        if (not re.fullmatch(r'[A-Za-z0-9._-]+', name) or path.is_symlink() or not path.is_file()
                or path.stat().st_size != value['size']
                or digest(path) != value['sha256']):
            raise ValueError('Prepared asset checksum mismatch')


# Call the fixed GitHub API endpoint and decode its response
def api(path):
    return json.loads(subprocess.check_output(['gh', 'api', '--hostname', 'github.com', path]))


# Download and validate a preparation artifact before exposing it for publication
def fetch(repository, run_id, output):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository) or any(p in ('.', '..') for p in repository.split('/')) or run_id < 1:
        raise ValueError('Invalid repository/run ID')
    prefix = f'repos/{repository}'
    run = api(f'{prefix}/actions/runs/{run_id}')
    workflow = api(f'{prefix}/actions/workflows/prepare-release.yml')
    if workflow.get('path') != WORKFLOW or type(workflow.get('id')) is not int or workflow['id'] < 1:
        raise ValueError('Unexpected preparation workflow identity')
    validate_run(run, repository, run_id, workflow['id'])
    inventory = api(f'{prefix}/actions/runs/{run_id}/artifacts?per_page=100')
    found = [a for a in inventory['artifacts'] if a['name'] == NAME]
    if inventory['total_count'] != len(inventory['artifacts']) or inventory['total_count'] > 100 or len(found) != 1 or found[0]['expired']:
        raise ValueError('Missing, ambiguous, or expired preparation artifact')
    artifact = found[0]
    if (type(artifact.get('id')) is not int or artifact['id'] < 1
            or artifact.get('workflow_run', {}).get('head_branch') != 'main'
            or artifact.get('workflow_run', {}).get('id') != run_id
            or artifact.get('workflow_run', {}).get('head_sha') != run['head_sha']
            or not 0 < artifact.get('size_in_bytes', 0) <= MAX_BYTES
            or not re.fullmatch('sha256:[a-f0-9]{64}', artifact.get('digest', ''))):
        raise ValueError('Artifact provenance/digest unavailable')
    output = Path(output)
    if output.exists():
        raise ValueError('Output must not exist')
    with tempfile.TemporaryDirectory() as tmp:
        archive = Path(tmp) / 'artifact.zip'
        with archive.open('wb') as sink:
            proc = subprocess.Popen(['gh', 'api', '--hostname', 'github.com',
                                     f'{prefix}/actions/artifacts/{artifact["id"]}/zip'], stdout=subprocess.PIPE)
            try:
                total = 0
                while chunk := proc.stdout.read(1024**2):
                    total += len(chunk)
                    if total > artifact['size_in_bytes']:
                        raise ValueError('Artifact download exceeds limit')
                    sink.write(chunk)
                if total != artifact['size_in_bytes']:
                    raise ValueError('Artifact download size mismatch')
                if proc.wait():
                    raise ValueError('Artifact download failed')
            finally:
                if proc.poll() is None:
                    proc.kill(); proc.wait()
                proc.stdout.close()
        if 'sha256:' + digest(archive) != artifact['digest']:
            raise ValueError('Artifact archive checksum mismatch')
        with zipfile.ZipFile(archive) as z:
            entries = z.infolist()
            if (len(entries) > 2050 or sum(e.file_size for e in entries) > MAX_BYTES
                    or len({e.filename for e in entries}) != len(entries)):
                raise ValueError('Artifact archive limits exceeded')
            for e in entries:
                if (not re.fullmatch(r'[A-Za-z0-9._-]+', e.filename)
                        or e.filename in ('.', '..') or e.file_size > 256 * 1024**2
                        or e.filename in ('catalog.json', 'release.json', 'provenance.json') and e.file_size > 4 * 1024**2
                        or (e.external_attr >> 16) & 0o170000 == 0o120000):
                    raise ValueError('Unsafe artifact entry')
            output.mkdir(parents=True)
            for e in entries:
                with z.open(e) as source, (output / e.filename).open('wb') as sink:
                    shutil.copyfileobj(source, sink, 1024**2)
        verify_bundle(output, run, repository)
        # Recheck conclusion and attempt after download to reject rerun races
        current = api(f'{prefix}/actions/runs/{run_id}')
        validate_run(current, repository, run_id, workflow['id'])
        if current['run_attempt'] != run['run_attempt'] or current['head_sha'] != run['head_sha']:
            raise ValueError('Preparation run changed during download')
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    s = sub.add_parser('seal'); s.add_argument('--directory', required=True)
    f = sub.add_parser('fetch'); f.add_argument('--repository', required=True)
    f.add_argument('--run-id', type=int, required=True); f.add_argument('--output', required=True)
    args = parser.parse_args()
    if args.action == 'seal': seal(args.directory)
    else: fetch(args.repository, args.run_id, args.output)
