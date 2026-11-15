import copy
import io
import zipfile
import hashlib
import json
import os
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import prepared_release as r
import go_modules


class PreparedReleaseTests(unittest.TestCase):
    # Construct a successful preparation-run fixture for provenance checks
    def run_record(self):
        return dict(id=7, repository={'full_name':'StreamDockManager/Plugins'},
                    head_repository={'full_name':'StreamDockManager/Plugins'}, workflow_id=5,
                    path=r.WORKFLOW, event='workflow_dispatch', head_branch='main',
                    status='completed', conclusion='success', head_sha='a'*40, run_attempt=1)

    # Verify provenance rejects other sources
    def test_provenance_rejects_other_sources(self):
        run = self.run_record()
        r.validate_run(run, 'StreamDockManager/Plugins', 7, 5)
        for key, value in [('id',8),('workflow_id',6),('path','other.yml'),('event','push'),
                           ('head_branch','release'),('status','in_progress'),('conclusion','failure'),
                           ('head_sha','abc'),('repository',{'full_name':'other/repo'}),
                           ('head_repository',{'full_name':'fork/repo'})]:
            bad = copy.deepcopy(run); bad[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                r.validate_run(bad, 'StreamDockManager/Plugins', 7, 5)

    # Verify bundle checks identity versions hashes attempt and files
    def test_bundle_checks_identity_versions_hashes_attempt_and_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp)
            c = json.dumps({'plugins':[{'id':'plugin','version':'0.1.0'}]}).encode()
            (d/'catalog.json').write_bytes(c)
            m = dict(repository='StreamDockManager/Plugins',commit='a'*40,channel='stable',
                     tag='plugins-stable-7', assets={'catalog.json':dict(size=len(c),sha256=hashlib.sha256(c).hexdigest())})
            (d/'release.json').write_text(json.dumps(m))
            env = dict(GITHUB_REPOSITORY=m['repository'],GITHUB_SHA=m['commit'],GITHUB_RUN_ID='7',GITHUB_RUN_ATTEMPT='1')
            with patch.dict(os.environ,env):r.seal(d)
            run = self.run_record();r.verify_bundle(d,run,m['repository'])
            for key,value in [('run_attempt',2),('head_sha','b'*40),('id',8)]:
                bad = dict(run);bad[key]=value
                with self.subTest(key=key),self.assertRaises(ValueError):r.verify_bundle(d,bad,m['repository'])
            (d/'extra').write_text('x')
            with self.assertRaises(ValueError):r.verify_bundle(d,run,m['repository'])
            (d/'extra').unlink();(d/'catalog.json').write_bytes(c+b' ')
            with self.assertRaises(ValueError):r.verify_bundle(d,run,m['repository'])

    # Verify fetch checks api and archive before accepting
    def test_fetch_checks_api_and_archive_before_accepting(self):
        run = self.run_record()
        repository = 'StreamDockManager/Plugins'
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp)/'source';d.mkdir()
            c = json.dumps({'plugins':[{'id':'plugin','version':'0.1.0'}]}).encode()
            (d/'catalog.json').write_bytes(c)
            m = dict(repository=repository,commit='a'*40,channel='stable',tag='plugins-stable-7',
                     assets={'catalog.json':dict(size=len(c),sha256=hashlib.sha256(c).hexdigest())})
            (d/'release.json').write_text(json.dumps(m))
            with patch.dict(os.environ,dict(GITHUB_REPOSITORY=repository,GITHUB_SHA='a'*40,GITHUB_RUN_ID='7',GITHUB_RUN_ATTEMPT='1')):
                r.seal(d)
            buf=io.BytesIO()
            with zipfile.ZipFile(buf,'w') as z:
                for f in d.iterdir():z.writestr(f.name,f.read_bytes())
            data=buf.getvalue()
            a=dict(id=9,name=r.NAME,expired=False,size_in_bytes=len(data),digest='sha256:'+hashlib.sha256(data).hexdigest(),
                   workflow_run=dict(id=7,head_sha='a'*40,head_branch='main'))
            class Process:
                # Wrap fixture artifact bytes as a completed download process
                def __init__(self,*args,**kwargs):self.stdout=io.BytesIO(data)
                # Return the mocked artifact-download process exit status
                def wait(self):return 0
                # Report the mocked artifact-download process as completed
                def poll(self):return 0
            cases = [('valid',None,None),('digest','digest','sha256:'+'b'*64),
                     ('size','size_in_bytes',len(data)+1),('id','id',0),('expired','expired',True),
                     ('workflow',None,None),('rerun',None,None),('count',None,None)]
            for name,key,value in cases:
                asset=copy.deepcopy(a)
                if key:asset[key]=value
                workflow=dict(id=5,path='wrong.yml' if name=='workflow' else r.WORKFLOW)
                final=dict(run,run_attempt=2) if name=='rerun' else run
                replies=[run,workflow,dict(total_count=2 if name=='count' else 1,artifacts=[asset]),final]
                with self.subTest(name=name),patch.object(r,'api',side_effect=replies),patch.object(r.subprocess,'Popen',Process):
                    if name=='valid':r.fetch(repository,7,Path(tmp)/name)
                    else:
                        with self.assertRaises(ValueError):r.fetch(repository,7,Path(tmp)/name)

    # Verify public sdk keeps standard resolution
    def test_public_sdk_keeps_standard_resolution(self):
        base = dict(GOENV='off', GOPROXY='https://proxy.golang.org,direct', GOSUMDB='sum.golang.org')
        result = go_modules.environment(base)
        self.assertEqual(result['STREAM_DOCK_MANAGER_SDK_REPOSITORY_URL'], go_modules.SDK_HTTPS)
        for key in go_modules.PRIVATE_KEYS:
            self.assertNotIn(key, result)
        self.assertNotIn('GIT_CONFIG_COUNT', result)
        for key,value in base.items():self.assertEqual(result[key], value)


if __name__ == '__main__':unittest.main()
