import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from development_go_workspace import create_workspace


class DevelopmentGoWorkspaceTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='sdk workspace test ')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.sdk = self.root / 'SDK checkout'
        (self.sdk / 'Go').mkdir(parents=True)
        (self.sdk / 'Go/go.mod').touch()
        self.commit = 'a' * 40
        self.windows = {'GOHOSTOS': 'windows', 'GOOS': 'windows',
                        'GOHOSTARCH': 'amd64', 'GOARCH': 'amd64'}

    def test_foreign_target_stops_before_checkout_and_go_work(self):
        wrong_target = dict(self.windows, GOOS='linux')
        with patch('development_go_workspace.subprocess.check_output',
                   return_value=json.dumps(wrong_target)) as command:
            with self.assertRaisesRegex(ValueError, 'Go host and target must match'):
                create_workspace(self.root, self.sdk, self.root, self.commit, 'windows', 'amd64')
        command.assert_called_once()
        self.assertFalse(list(self.root.glob('plugin-sdk-*')))

    def test_rejects_sdk_checkout_that_does_not_match_exact_commit(self):
        with patch('development_go_workspace.subprocess.check_output',
                   side_effect=[json.dumps(self.windows), 'b' * 40]) as command:
            with self.assertRaisesRegex(ValueError, 'exact requested SDK commit'):
                create_workspace(self.root, self.sdk, self.root, self.commit, 'windows', 'amd64')
        self.assertEqual(command.call_count, 2)
        self.assertFalse(list(self.root.glob('plugin-sdk-*')))
