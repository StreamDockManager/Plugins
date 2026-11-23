#!/usr/bin/env python3
"""Prepare all-plugin GitHub release assets; publish only via explicit subcommand.

No GitHub calls occur during preparation. CDN publication remains independent.
"""
import argparse
import base64
from copy import deepcopy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

import catalog
import unified_packages

ROOT = catalog.ROOT
LIMIT = 256 * 1024 * 1024


# Serialize release metadata deterministically as UTF-8 JSON
def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


# Validate repository, tag, channel, and exact source commit identity
def identity(repository, tag, commit, channel):
    if not isinstance(repository,str) or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository) or any(part in ('.','..') for part in repository.split('/')):
        raise ValueError('Expected GitHub owner/repository')
    if channel not in ('stable', 'development') or not re.fullmatch(r'plugins-' + channel + r'-[A-Za-z0-9][A-Za-z0-9._-]*', tag):
        raise ValueError('Tag must use plugins-<channel>-<suffix>')
    if not re.fullmatch(r'[a-f0-9]{40}', commit):
        raise ValueError('An exact 40-character source commit is required')


# Build catalog entries pointing to immutable GitHub release assets
def release_catalog(records, inventories, hosts, repository, tag, commit, channel, directory):
    identity(repository, tag, commit, channel)
    result = catalog.empty(repository, channel)
    files = {}
    # Register one release artifact and reject filename collisions
    def artifact(package, filename, path, keys):
        if filename in files:
            raise ValueError('Release asset filename collision')
        files[filename] = Path(path)
        value = {key: deepcopy(package[key]) for key in keys}
        value.update(filename=filename, release_tag=tag, source_commit=commit,
                     url=f'https://github.com/{repository}/releases/download/{tag}/{filename}')
        return value
    for source in sorted(records, key=lambda item: item['id']):
        plugin = deepcopy(source)
        plugin.pop('source_fingerprint', None)
        plugin['downloads'] = []
        for platform, inventory in inventories.items():
            if platform not in catalog.supported_platforms(plugin):
                continue
            for package in catalog.selected_packages(inventory['packages'], plugin['id'], platform):
                value = artifact(package, catalog.artifact_filename(plugin, package), catalog.package_path(directory, package), ('os','architectures','sha256','size','files'))
                plugin['downloads'].append(value)
        for host, field, suffix in [('manager','unified_download','unified'),('opendeck','opendeck_download','opendeck-universal')]:
            package = next((p for p in hosts[host]['packages'] if p['id']==plugin['id']), None)
            if package:
                value = artifact(package, f'{plugin["id"]}-{plugin["version"]}-{suffix}.zip', unified_packages.path_for(directory,plugin['id'],host), ('targets','sha256','size','files'))
                plugin[field]=value
        result['plugins'].append(plugin)
    catalog.validate(result, catalog_url=f'https://api.github.com/repos/{repository}/releases')
    return result, files


# Compile the host packaging tool once and reuse it for each target
def build_packages(env, jobs, macos_intel=False):
    with tempfile.TemporaryDirectory(prefix='plugins-release-tool-') as temporary:
        tool = Path(temporary) / ('pluginbuild.exe' if os.name == 'nt' else 'pluginbuild')
        subprocess.run(['go', 'build', '-p', '1', '-o', str(tool), './tools/pluginbuild'],
                       cwd=ROOT, env=env, check=True)
        records = catalog.source_records(env=env, builder=tool)
        if not records:
            raise ValueError('No source plugins')
        for platform in ('linux', 'windows', 'darwin'):
            args = [str(tool), 'package', '--os', platform, '--jobs', str(jobs)]
            if macos_intel:
                args.append('--macos-intel')
            subprocess.run(args, cwd=ROOT, env=env, check=True)
        return records


# Stage every verified release asset from the exact clean source revision
def prepare(directory, output, repository, tag, commit, channel='stable', build=False, jobs=4, macos_intel=False):
    identity(repository,tag,commit,channel)
    if not 1 <= jobs <= 128:
        raise ValueError('jobs must be between 1 and 128')
    # Require the exact clean source revision before and after release staging
    def clean_commit():
        actual=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
        dirty=subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True).strip()
        if actual != commit or dirty:
            raise ValueError('Release preparation requires the exact committed, clean source checkout')
    clean_commit()
    directory, output = Path(directory), Path(output) if output else ROOT/'dist/github-release'/tag
    if output.exists() or output.is_symlink():
        raise ValueError('Release preparation output already exists; choose a fresh directory')
    # Always discover the entire repository, never honor PLUGIN filtering
    env=dict(os.environ,RELEASE_REPOSITORY=repository,GOMAXPROCS=str(jobs),PLUGIN_MACOS_INTEL='1' if macos_intel else '0')
    env.pop('PLUGIN',None)
    if build:
        if directory.resolve() != (ROOT/'dist').resolve():
            raise ValueError('--build requires this checkout dist directory')
        records = build_packages(env, jobs, macos_intel)
    else:
        records = catalog.source_records(env=env)
        if not records:
            raise ValueError('No source plugins')
    inventories={}
    hosts=unified_packages.build_hosts(directory,records,macos_intel=macos_intel,validated_inventories=inventories)
    document, files=release_catalog(records,inventories,hosts,repository,tag,commit,channel,directory)
    output.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.github-release-',dir=output.parent) as temporary:
        staging=Path(temporary)/'assets';staging.mkdir()
        catalog_bytes=encoded(document)
        if len(catalog_bytes)>4*1024*1024: raise ValueError('Release catalog exceeds 4 MiB')
        (staging/'catalog.json').write_bytes(catalog_bytes)
        for name,path in files.items():
            shutil.copyfile(path,staging/name)
        metadata={name:{'sha256':catalog.digest(path),'size':path.stat().st_size} for name,path in sorted((p.name,p) for p in staging.iterdir())}
        expected={a['filename']:a for p in document['plugins'] for a in [*p['downloads'],p.get('unified_download'),p.get('opendeck_download')] if a}
        for name,value in expected.items():
            if metadata[name] != {key:value[key] for key in ('sha256','size')}:
                raise ValueError('Package changed during release staging')
        (staging/'release.json').write_bytes(encoded(dict(schema_version=1,repository=repository,tag=tag,commit=commit,channel=channel,assets=metadata)))
        clean_commit()
        staging.rename(output)
    return output


class GitHub:
    """gh CLI adapter. All mutation is confined to explicit publish()."""
    # Call the fixed GitHub API endpoint and decode its response
    def api(self, endpoint, data=None, missing=False):
        args=['gh','api','--hostname','github.com',endpoint]
        if data is not None: args+=['--method','POST','--input','-']
        result=subprocess.run(args,input=json.dumps(data) if data is not None else None,text=True,capture_output=True)
        if result.returncode:
            if missing and '(HTTP 404)' in result.stderr: return None
            raise ValueError('GitHub API request failed: '+result.stderr.strip())
        return json.loads(result.stdout)
    # Upload a missing release asset without overwriting existing bytes
    def upload(self, repository, tag, path):
        subprocess.run(['gh','release','upload',tag,str(path),'--repo','github.com/'+repository],check=True) # no --clobber
    # Download a release asset while enforcing the maximum byte count
    def download(self, repository, asset, path):
        with path.open('wb') as output:
            process=subprocess.Popen(['gh','api','--hostname','github.com',f'repos/{repository}/releases/assets/{asset["id"]}','-H','Accept: application/octet-stream'],stdout=subprocess.PIPE)
            total=0
            try:
                while chunk:=process.stdout.read(1024*1024):
                    total+=len(chunk)
                    if total>LIMIT: raise ValueError('Release asset exceeds bound')
                    output.write(chunk)
                if process.wait(): raise ValueError('GitHub asset download failed')
            finally:
                if process.poll() is None: process.kill();process.wait()
                process.stdout.close()
    # Verify staged and remote bytes before publishing an immutable GitHub release
    def publish(self, repository, release_id, latest):
        subprocess.run(['gh','api','--hostname','github.com',f'repos/{repository}/releases/{release_id}','--method','PATCH','--input','-'],input=json.dumps({'draft':False,'make_latest':'true' if latest else 'false'}),text=True,check=True)


# Verify staged and remote bytes before publishing an immutable GitHub release
def publish(directory=None, github=None, tag=None, repository=None):
    if directory is None:
        if tag is None: raise ValueError('Specify --directory or --tag')
        identity(repository or 'StreamDockManager/Plugins',tag,'0'*40,'development' if tag.startswith('plugins-development-') else 'stable')
        directory=ROOT/'dist/github-release'/tag
    requested_tag, requested_repository=tag,repository
    directory=Path(directory).resolve()
    manifest=json.loads((directory/'release.json').read_text())
    repository,tag,commit,channel=(manifest[key] for key in ('repository','tag','commit','channel'))
    identity(repository,tag,commit,channel)
    if (requested_tag is not None and requested_tag!=tag) or (requested_repository is not None and requested_repository!=repository):
        raise ValueError('Requested release identity differs from staging')
    assets=manifest['assets']
    if manifest.get('schema_version')!=1 or not isinstance(assets,dict) or not assets or len(assets)>2048 or 'catalog.json' not in assets:
        raise ValueError('Invalid release staging manifest')
    for name,value in assets.items():
        if not re.fullmatch(r'[A-Za-z0-9._-]+',name) or name=='release.json' or not (name.endswith('.zip') or name=='catalog.json'):
            raise ValueError('Unsafe release asset name')
        path=directory/name
        if path.is_symlink() or not path.is_file() or not 0<value['size']<=LIMIT or path.stat().st_size!=value['size'] or catalog.digest(path)!=value['sha256']:
            raise ValueError('Release staging bytes changed')
    if assets['catalog.json']['size']>4*1024*1024: raise ValueError('Release catalog exceeds 4 MiB')
    document=json.loads((directory/'catalog.json').read_text())
    catalog.validate(document,catalog_url=f'https://api.github.com/repos/{repository}/releases')
    if not document.get('plugins') or document['repository']!=repository or document['channel']!=channel:
        raise ValueError('Release catalog identity differs')
    expected={'catalog.json'}
    for plugin in document['plugins']:
        for item in [*plugin['downloads'],plugin.get('unified_download'),plugin.get('opendeck_download')]:
            if not item: continue
            if item['release_tag']!=tag or item['source_commit']!=commit or assets.get(item['filename'])!={k:item[k] for k in ('size','sha256')}:
                raise ValueError('Release catalog artifact differs from staged bytes')
            expected.add(item['filename'])
    if expected!=set(assets): raise ValueError('Unexpected or missing staged release asset')
    github=github or GitHub()
    # Verify an immutable source tag or create it only when allowed
    def check_tag(create=False):
        ref=github.api(f'repos/{repository}/git/ref/tags/{tag}',missing=True)
        if ref is None:
            if not create: raise ValueError('Release tag disappeared')
            github.api(f'repos/{repository}/git/refs',{'ref':'refs/tags/'+tag,'sha':commit});return
        obj=ref['object']
        for _ in range(4):
            if obj['type']=='commit': break
            if obj['type']!='tag': raise ValueError('Unsupported tag target')
            obj=github.api(f'repos/{repository}/git/tags/{obj["sha"]}')['object']
        if obj.get('type')!='commit' or obj['sha']!=commit: raise ValueError('Existing tag points at another commit; never retagged')
    catalog_bytes=(directory/'catalog.json').read_bytes()
    blob_sha=hashlib.sha1(b'blob '+str(len(catalog_bytes)).encode()+b'\0'+catalog_bytes).hexdigest()
    catalog_sha=assets['catalog.json']['sha256']
    marker='StreamDock plugin release v1\n'+hashlib.sha256(encoded(manifest)).hexdigest()+f'\n<!-- sdm-catalog-blob:{blob_sha} -->\n<!-- sdm-catalog-sha256:{catalog_sha} -->'
    release=github.api(f'repos/{repository}/releases/tags/{tag}',missing=True)
    if release is not None and (release.get('body')!=marker or release.get('prerelease')!=(channel=='development') or release.get('tag_name')!=tag):
        raise ValueError('Existing release differs; never overwritten')
    check_tag(create=release is None or release.get('draft',False))
    # A separate, never-moved metadata tag keeps the exact catalog blob reachable
    # for CORS-safe browser API reads without touching the source release tag
    metadata_tag='catalog-'+tag
    metadata_ref=github.api(f'repos/{repository}/git/ref/tags/{metadata_tag}',missing=True)
    if metadata_ref is None:
        if release is not None and not release['draft']:
            raise ValueError('Published catalog metadata tag is missing; never repaired implicitly')
        blob=github.api(f'repos/{repository}/git/blobs',{'content':base64.b64encode(catalog_bytes).decode('ascii'),'encoding':'base64'})
        if blob.get('sha')!=blob_sha: raise ValueError('GitHub catalog blob differs from release asset')
        tree=github.api(f'repos/{repository}/git/trees',{'tree':[{'path':'catalog.json','mode':'100644','type':'blob','sha':blob_sha}]})
        metadata_commit=github.api(f'repos/{repository}/git/commits',{'message':'Catalog metadata for '+tag,'tree':tree['sha'],'parents':[]})
        github.api(f'repos/{repository}/git/refs',{'ref':'refs/tags/'+metadata_tag,'sha':metadata_commit['sha']})
    else:
        obj=metadata_ref['object']
        if obj.get('type')!='commit': raise ValueError('Catalog metadata tag must directly reference its commit')
        metadata_commit=github.api(f'repos/{repository}/git/commits/{obj["sha"]}')
        tree=github.api(f'repos/{repository}/git/trees/{metadata_commit["tree"]["sha"]}')
        members=tree.get('tree',[])
        if tree.get('truncated') or len(members)!=1 or any(item.get(key)!=value for key,value in {'path':'catalog.json','mode':'100644','type':'blob','sha':blob_sha}.items() for item in members):
            raise ValueError('Existing catalog metadata tag differs; never retagged')
        blob=github.api(f'repos/{repository}/git/blobs/{blob_sha}')
        if blob.get('encoding')!='base64' or blob.get('size')!=len(catalog_bytes) or base64.b64decode(blob.get('content','').replace('\n',''),validate=True)!=catalog_bytes:
            raise ValueError('Existing catalog metadata bytes differ')
    if release is None:
        release=github.api(f'repos/{repository}/releases',dict(tag_name=tag,target_commitish=commit,name=tag,body=marker,draft=True,prerelease=channel=='development'))
    if release.get('body')!=marker or release.get('prerelease')!=(channel=='development') or release.get('tag_name')!=tag:
        raise ValueError('Existing release differs; never overwritten')
    # Read the remote release asset inventory for comparison
    def remote_assets():
        values=github.api(f'repos/{repository}/releases/{release["id"]}/assets?per_page=100&page=1')
        for page in range(2,22):
            if len(values)%100: break
            batch=github.api(f'repos/{repository}/releases/{release["id"]}/assets?per_page=100&page={page}')
            if not batch: break
            values.extend(batch)
        if len(values)>2048 or len({a['name'] for a in values})!=len(values) or any(a['name'] not in assets for a in values):
            raise ValueError('Unexpected or duplicate remote release assets')
        return {a['name']:a for a in values}
    # Download remote assets and compare their bytes with the staged inventory
    def verify_remote(existing):
        with tempfile.TemporaryDirectory(prefix='verify-github-release-') as temporary:
            for name,item in existing.items():
                if item.get('size')!=assets[name]['size']: raise ValueError('Existing release asset size differs')
                if name=='catalog.json' and item.get('digest')!='sha256:'+catalog_sha:
                    raise ValueError('GitHub catalog asset SHA256 digest is missing or differs')
                path=Path(temporary)/name
                github.download(repository,item,path)
                if catalog.digest(path)!=assets[name]['sha256']: raise ValueError('Existing release asset bytes differ; never overwritten')
    existing=remote_assets()
    verify_remote(existing)
    for name in sorted(assets):
        if name not in existing:
            if not release['draft']: raise ValueError('Published release is incomplete; never mutated')
            github.upload(repository,tag,directory/name)
    existing=remote_assets()
    if set(existing)!=set(assets): raise ValueError('Release upload inventory incomplete')
    verify_remote(existing)
    check_tag()
    if release['draft']:
        github.publish(repository,release['id'],channel=='stable')
        completed=github.api(f'repos/{repository}/releases/tags/{tag}')
        if completed.get('draft') is not False or completed.get('id')!=release['id']:
            raise ValueError('Release publication not confirmed')
    return f'https://github.com/{repository}/releases/tag/{tag}'


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    sub=parser.add_subparsers(dest='command',required=True)
    command=sub.add_parser('prepare')
    command.add_argument('--directory',type=Path,default=ROOT/'dist')
    command.add_argument('--output',type=Path)
    command.add_argument('--repository',default=os.environ.get('RELEASE_REPOSITORY','StreamDockManager/Plugins'))
    command.add_argument('--tag',required=True);command.add_argument('--commit',required=True)
    command.add_argument('--channel',choices=('stable','development'),default='stable')
    command.add_argument('--build',action='store_true');command.add_argument('--jobs',type=int,default=4)
    command.add_argument('--macos-intel',action='store_true')
    command=sub.add_parser('publish');command.add_argument('--directory',type=Path)
    command.add_argument('--tag');command.add_argument('--repository')
    args=vars(parser.parse_args());action=args.pop('command')
    print(prepare(**args) if action=='prepare' else publish(**args))
