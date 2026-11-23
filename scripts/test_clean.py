"""Disposable cleanup regressions; never cleans the checkout."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('repo_clean', Path(__file__).with_name('clean.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CleanTests(unittest.TestCase):
    def test_cleanup_dry_run_and_idempotence(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            for name in ('dist/output', 'node_modules/package/source', '.cache/signed-dev-data/db', 'source'):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('preserve or generated')
            module.clean(root, dry_run=True)
            self.assertTrue((root / 'dist/output').exists())
            module.clean(root)
            module.clean(root)
            self.assertFalse((root / 'dist').exists())
            for name in ('node_modules/package/source', '.cache/signed-dev-data/db', 'source'):
                self.assertTrue((root / name).exists())

    def test_preflight_rejects_tracked_and_links_without_partial_cleanup(self):
        for unsafe in ('tracked', 'link', 'ancestor', 'nested'):
            with self.subTest(unsafe=unsafe), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                subprocess.run(['git', 'init', '-q', str(root)], check=True)
                (root / 'dist').mkdir()
                (root / 'dist/output').write_text('keep on failure')
                if unsafe == 'tracked':
                    subprocess.run(['git', '-C', str(root), 'add', 'dist/output'], check=True)
                elif unsafe == 'link':
                    (root / 'target').symlink_to(root / 'outside', target_is_directory=True)
                elif unsafe == 'ancestor':
                    (root / '.cache').symlink_to(root / 'outside', target_is_directory=True)
                else:
                    (root / 'dist/.git').mkdir()
                with self.assertRaises(ValueError):
                    module.clean(root)
                self.assertTrue((root / 'dist/output').exists())

    def test_nested_links_are_unlinked_without_following_targets(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'repo'
            outside = Path(temporary) / 'toolchain'
            root.mkdir()
            outside.mkdir()
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            # A linked external checkout is not a nested owned checkout.
            (outside / '.git').mkdir()
            sentinel = outside / 'keep'
            sentinel.write_text('external toolchain')
            output = root / 'dist/.build'
            output.mkdir(parents=True)
            (output / 'goroot').symlink_to(outside, target_is_directory=True)
            (output / 'file').symlink_to(sentinel)
            (output / 'broken').symlink_to(outside / 'missing')
            (output / 'loop').symlink_to(root / 'dist', target_is_directory=True)
            module.clean(root, dry_run=True)
            self.assertTrue((output / 'goroot').is_symlink())
            module.clean(root)
            self.assertFalse((root / 'dist').exists())
            self.assertEqual(sentinel.read_text(), 'external toolchain')
            self.assertTrue((outside / '.git').is_dir())

    def test_custom_data_is_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            (root / 'dist').mkdir()
            (root / 'dist/db').write_text('data')
            with patch.dict(os.environ, {'MANAGER_DATA_DIR': str(root / 'dist')}):
                module.clean(root)
            self.assertTrue((root / 'dist/db').exists())


if __name__ == '__main__':
    unittest.main()
