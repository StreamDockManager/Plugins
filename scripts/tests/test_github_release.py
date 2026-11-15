import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import github_release as release


class FakeGitHub:
    # Initialize FakeGitHub fixture state
    def __init__(self):
        self.tag=None;self.release=None;self.assets={};self.writes=[];self.fail_upload=False;self.metadata=None;self.blob=None;self.tree=None;self.blob_raw=None
    # Handle the mocked GitHub API request
    def api(self,path,data=None,missing=False):
        if '/git/ref/tags/catalog-' in path:
            return {'object':{'type':'commit','sha':self.metadata}} if self.metadata else None
        if '/git/ref/tags/' in path:
            return {'object':{'type':'commit','sha':self.tag}} if self.tag else None
        if path.endswith('/git/refs'):
            if data['ref'].startswith('refs/tags/catalog-'): self.metadata=data['sha']
            else: self.tag=data['sha']
            self.writes.append('tag');return {}
        if path.endswith('/git/blobs'):
            raw=base64.b64decode(data['content']);self.blob_raw=raw;self.blob=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest();return {'sha':self.blob}
        if '/git/blobs/' in path: return {'encoding':'base64','size':len(self.blob_raw),'content':base64.b64encode(self.blob_raw).decode()}
        if path.endswith('/git/trees'): self.tree=data['tree'];return {'sha':'tree'}
        if path.endswith('/git/commits'): return {'sha':'metadata'}
        if '/git/commits/' in path: return {'tree':{'sha':'tree'}}
        if '/git/trees/' in path: return {'tree':self.tree}
        if '/releases/tags/' in path: return self.release
        if path.endswith('/releases'):
            self.release=dict(data,id=1);self.writes.append('draft');return self.release
        if '/assets?' in path:
            return [{'name':name,'id':i,'size':len(raw),'digest':'sha256:'+hashlib.sha256(raw).hexdigest()} for i,(name,raw) in enumerate(self.assets.items())]
        raise AssertionError(path)
    # Record an uploaded fixture asset without contacting GitHub
    def upload(self,repository,tag,path):
        if self.fail_upload: raise ValueError('upload interrupted')
        self.assets[path.name]=path.read_bytes();self.writes.append('upload:'+path.name)
    # Copy the fixture remote asset to the requested local path
    def download(self,repository,asset,path): path.write_bytes(self.assets[asset['name']])
    # Mark the fixture release as published without remote side effects
    def publish(self,repository,release_id,latest):
        self.release['draft']=False;self.writes.append('publish');self.latest=latest


class GitHubReleaseTests(unittest.TestCase):
    # Create a local release bundle fixture with matching catalog and checksums
    def staging(self,folder,channel='stable'):
        folder=Path(folder);tag=f'plugins-{channel}-fixture';repo='test/plugins';commit='a'*40
        name='test.plugin-0.1.0-linux-amd64.zip';raw=b'fixture zip'
        (folder/name).write_bytes(raw)
        artifact={'filename':name,'release_tag':tag,'source_commit':commit,'sha256':hashlib.sha256(raw).hexdigest(),'size':len(raw)}
        document={'repository':repo,'channel':channel,'plugins':[{'downloads':[artifact]}]}
        (folder/'catalog.json').write_bytes(release.encoded(document))
        metadata={p.name:{'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'size':p.stat().st_size} for p in folder.iterdir()}
        manifest={'schema_version':1,'repository':repo,'tag':tag,'commit':commit,'channel':channel,'assets':metadata}
        (folder/'release.json').write_bytes(release.encoded(manifest))
        return manifest

    # Verify prepare does not contact github and requires clean exact commit
    def test_prepare_does_not_contact_github_and_requires_clean_exact_commit(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.subprocess,'check_output',return_value='b'*40), patch.object(release,'GitHub') as github:
            with self.assertRaisesRegex(ValueError,'exact committed'):
                release.prepare(folder,Path(folder)/'out','test/plugins','plugins-stable-fixture','a'*40)
            github.assert_not_called()

    # Verify create verify publish and identical retry is readonly
    def test_create_verify_publish_and_identical_retry_is_readonly(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate') as validate:
            self.staging(folder);github=FakeGitHub()
            release.publish(folder,github)
            self.assertEqual(github.tag,'a'*40)
            self.assertEqual(github.writes[-1],'publish');self.assertTrue(github.latest)
            self.assertFalse(github.release['draft']);self.assertEqual(set(github.assets),{'catalog.json','test.plugin-0.1.0-linux-amd64.zip'})
            validate.assert_called()
            github.writes.clear();release.publish(folder,github);self.assertEqual(github.writes,[])

    # Verify conflicting tag and changed local bytes prevent writes
    def test_conflicting_tag_and_changed_local_bytes_prevent_writes(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder);github=FakeGitHub();github.tag='b'*40
            with self.assertRaisesRegex(ValueError,'another commit'):release.publish(folder,github)
            self.assertEqual(github.writes,[])
            (Path(folder)/'catalog.json').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError,'staging bytes'):release.publish(folder,github)
            self.assertEqual(github.writes,[])

    # Verify interrupted draft resumes but corrupt existing asset never overwritten
    def test_interrupted_draft_resumes_but_corrupt_existing_asset_never_overwritten(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder);github=FakeGitHub();github.fail_upload=True
            with self.assertRaisesRegex(ValueError,'interrupted'):release.publish(folder,github)
            self.assertTrue(github.release['draft'])
            github.fail_upload=False;release.publish(folder,github)
            github.assets['test.plugin-0.1.0-linux-amd64.zip']=b'corrupt zip'
            github.writes.clear()
            with self.assertRaisesRegex(ValueError,'differs|differ'):release.publish(folder,github)
            self.assertEqual(github.writes,[])

    # Verify development is prerelease and not latest
    def test_development_is_prerelease_and_not_latest(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder,'development');github=FakeGitHub();release.publish(folder,github)
            self.assertTrue(github.release['prerelease']);self.assertFalse(github.latest)

    # Verify unrelated existing release not modified
    def test_unrelated_existing_release_not_modified(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder);github=FakeGitHub();github.tag='a'*40
            manifest=json.loads((Path(folder)/'release.json').read_text())
            raw=(Path(folder)/'catalog.json').read_bytes();sha=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()
            github.metadata='metadata';github.tree=[{'path':'catalog.json','mode':'100644','type':'blob','sha':sha}]
            github.release={'body':'someone else','prerelease':False,'tag_name':'plugins-stable-fixture','id':1,'draft':True}
            with self.assertRaisesRegex(ValueError,'Existing release differs'):release.publish(folder,github)
            self.assertEqual(github.writes,[])

    # Verify metadata tag and catalog digest are required
    def test_metadata_tag_and_catalog_digest_are_required(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder);github=FakeGitHub();release.publish(folder,github)
            self.assertIn('<!-- sdm-catalog-blob:',github.release['body'])
            self.assertIn('<!-- sdm-catalog-sha256:',github.release['body'])
            github.writes.clear();github.tree[0]['sha']='f'*40
            with self.assertRaisesRegex(ValueError,'metadata tag differs'):release.publish(folder,github)
            self.assertEqual(github.writes,[])
        with tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
            self.staging(folder);github=FakeGitHub();original=github.api
            # Simulate a remote asset missing its required integrity digest
            def missing_digest(path,*args,**kwargs):
                result=original(path,*args,**kwargs)
                if '/assets?' in path:
                    for item in result:item.pop('digest',None)
                return result
            github.api=missing_digest
            with self.assertRaisesRegex(ValueError,'digest is missing'):release.publish(folder,github)
            self.assertTrue(github.release['draft']);self.assertNotIn('publish',github.writes)

    # Verify invalid repository is rejected before any github request
    def test_invalid_repository_is_rejected_before_any_github_request(self):
        for repository in ('../plugins','test/..','./plugins','test/.','test/plugins/extra','test/%2e%2e','https://evil.invalid/repo'):
            with self.subTest(repository=repository), tempfile.TemporaryDirectory() as folder, patch.object(release.catalog,'validate'):
                manifest=self.staging(folder);manifest['repository']=repository
                (Path(folder)/'release.json').write_bytes(release.encoded(manifest))
                github=FakeGitHub()
                with patch.object(github,'api',wraps=github.api) as api:
                    with self.assertRaisesRegex(ValueError,'owner/repository'):release.publish(folder,github)
                    api.assert_not_called()
