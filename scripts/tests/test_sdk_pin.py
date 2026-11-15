import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import sdk_pin


class ReleasePinTests(unittest.TestCase):
    # Reject moving refs, pseudo versions and unsupported module-major paths
    def test_version_policy(self):
        for value in ('main', 'latest', 'Go/v0.1.0', 'v2.0.0', 'v0.0.0-20261113231633-a07536dd5d79', 'v0.1.1-0.20261113231633-a07536dd5d79', 'v1.2.3-01', 'v1.2.3-a..b'):
            with self.assertRaises(ValueError):
                sdk_pin.release_version(value)
        for value in ('v0.1.0', 'v1.2.3', 'v0.2.0-rc.1'):
            self.assertEqual(sdk_pin.release_version(value), value)

    # Resolve a release against staged files while recording only its two checksums
    def exercise(self, failure=None, action='update'):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = {'Require': [{'Path': sdk_pin.MODULE, 'Version': 'v0.1.0'}, {'Path': 'other/module', 'Version': 'v1.0.0'}]}
            (root / 'go.mod').write_text(json.dumps(original))
            (root / 'go.sum').write_text('other/module v1.0.0 h1:original\n')
            before = [(root / name).read_bytes() for name in ('go.mod', 'go.sum')]

            # Emulate Go only against the alternate module file supplied by the updater
            def fake_go(args, mod, env, cwd):
                if args[0] == 'edit':
                    value = json.loads(mod.read_text())
                    if failure == 'unrelated' and value['Require'][0]['Version'] == 'v0.2.0':
                        value['Require'][1]['Version'] = 'v2.0.0'
                    return value
                if failure == 'download':
                    raise RuntimeError('release unavailable')
                version = args[-1].split('@')[1]
                sums = mod.with_suffix('.sum')
                with sums.open('a') as out:
                    out.write(f'{sdk_pin.MODULE} {version} h1:module\n{sdk_pin.MODULE} {version}/go.mod h1:mod\n')
                return {'Path': sdk_pin.MODULE, 'Version': version, 'Sum': 'h1:module', 'GoModSum': 'h1:mod',
                        'Origin': {'Ref': 'refs/heads/main' if failure == 'origin' else 'refs/tags/Go/' + version}}

            # Emulate the explicit requirement edit without touching the repository files
            def fake_run(args, **kwargs):
                mod = Path(next(a.split('=', 1)[1] for a in args if a.startswith('-modfile=')))
                value = json.loads(mod.read_text())
                value['Require'][0]['Version'] = 'v0.2.0'
                mod.write_text(json.dumps(value))

            real_replace = sdk_pin.os.replace
            calls = 0
            # Fail the second replacement to verify rollback of the first file
            def replace(source, target):
                nonlocal calls
                calls += 1
                if failure == 'replace' and calls == 2:
                    raise OSError('fixture replacement failure')
                return real_replace(source, target)

            with patch.object(sdk_pin, 'go', fake_go), patch.object(sdk_pin.subprocess, 'run', fake_run), patch.object(sdk_pin.go_modules, 'environment', return_value={}), patch.object(sdk_pin.os, 'replace', replace):
                if failure or action == 'check':
                    with self.assertRaises((ValueError, RuntimeError, OSError)):
                        sdk_pin.pin(action, 'v0.2.0', root)
                    self.assertEqual(before, [(root / name).read_bytes() for name in ('go.mod', 'go.sum')])
                else:
                    self.assertEqual(sdk_pin.pin(action, 'v0.2.0', root), 'v0.2.0')
                    self.assertEqual(json.loads((root / 'go.mod').read_text())['Require'][1], original['Require'][1])
                    self.assertIn(before[1], (root / 'go.sum').read_bytes())

    # Preserve unrelated requirements and existing checksums during a successful release update
    def test_update(self):
        self.exercise()

    # Leave module files unchanged when download, validation or replacement fails
    def test_failures_leave_files_unchanged(self):
        for failure in ('download', 'unrelated', 'replace', 'origin'):
            with self.subTest(failure=failure):
                self.exercise(failure)

    # Reject a mismatched current release without rewriting tracked files
    def test_check_is_read_only(self):
        self.exercise(action='check')

    # Require a tagged SDK release before preparing distributable packages
    def test_release_preparation_checks_pin(self):
        source = (sdk_pin.ROOT / 'scripts/prepare_ci.py').read_text()
        self.assertIn("['python3', 'scripts/sdk_pin.py', 'check']", source)
        self.assertLess(source.index("'scripts/sdk_pin.py'"), source.index("'scripts/github_release.py'"))

    # Reject SDK replacement directives rather than verifying different local source
    def test_replacement_rejected(self):
        with self.assertRaises(ValueError):
            sdk_pin.requirement({'Replace': [{'Old': {'Path': sdk_pin.MODULE}}]})
