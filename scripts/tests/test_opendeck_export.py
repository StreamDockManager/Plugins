import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile
import zlib
import struct

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build
import catalog
import unified_packages as unified


class OpenDeckExportTests(unittest.TestCase):
    # Create isolated package inputs for the exporter regression
    def fixture(self, root):
        record = {'id': 'test.counter', 'version': '0.1.0', 'required_capabilities': [], 'capabilities': {'theme_protocol': 1}}
        inventories = {}
        for osname, platform, arch in [('windows', 'windows', 'amd64'), ('linux', 'linux', 'amd64'), ('macos', 'mac', 'arm64')]:
            entry = 'bin/counter' + ('.exe' if osname == 'windows' else '')
            manifest = {'UUID': record['id'], 'Version': record['version'], 'CodePath': entry,
                        'OS': [{'Platform': platform}], 'Icon': 'icon.png',
                        'StreamDockManager': {'Capabilities': record['capabilities']},
                        'Actions': [{'UUID': 'test.counter.action', 'Controllers': ['Keypad', 'Knob'], 'States': [{'Image': 'icon.png'}]}]}
            files = {'manifest.json': json.dumps(manifest).encode(), entry: osname.encode(), 'icon.png': b'original PNG', 'icon.svg': b'different SVG'}
            folder = 'darwin' if osname == 'macos' else osname
            relative = f'packages/{folder}/{arch}/test.counter.zip'
            path = root / relative
            unified.write_archive(path, record['id'], files, {entry})
            inventories[osname] = {'packages': [{'id': record['id'], 'version': record['version'], 'architectures': [arch], 'os': osname,
                'path': relative, 'sha256': catalog.digest(path), 'size': path.stat().st_size,
                'files': {name: hashlib.sha256(raw).hexdigest() for name, raw in files.items()}}]}
        return record, inventories

    # Verify universal preserves capabilities images and separate output
    def test_universal_preserves_capabilities_images_and_separate_output(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(catalog, 'validate_compiled_capabilities'), patch.object(unified.validate_packages, 'validate_executable'):
            root = Path(folder)
            record, inventories = self.fixture(root)
            files, executables, targets = unified.assemble(root, record, inventories, 'opendeck')
            manifest = json.loads(files['manifest.json'])
            self.assertEqual({item['os'] for item in targets}, {'macos', 'windows', 'linux'})
            self.assertEqual(len(manifest['CodePaths']), 3)
            self.assertIn('CodePathLin', manifest)
            self.assertNotIn('CodePathLinux', manifest)
            self.assertEqual(manifest['StreamDockManager']['Capabilities'], record['capabilities'])
            self.assertEqual(files[manifest['Icon'] + '.png'], b'original PNG')
            self.assertEqual(manifest['Actions'][0]['States'][0]['Image'], manifest['Icon'])
            self.assertEqual(manifest['Actions'][0]['Controllers'], ['Keypad', 'Encoder'])
            self.assertEqual(manifest['Actions'][0]['Encoder'], {})
            path = unified.path_for(root, record['id'], 'opendeck')
            self.assertNotEqual(path, unified.path_for(root, record['id']))
            unified.write_archive(path, record['id'], files, executables, 'opendeck')
            with zipfile.ZipFile(path) as archive:
                self.assertTrue(all(entry.compress_type == zipfile.ZIP_DEFLATED for entry in archive.infolist()))
            original, _, _ = unified.assemble(root, record, inventories)
            self.assertEqual(json.loads(original['manifest.json'])['Icon'], 'icon.png')
            self.assertNotIn('CodePathLin', json.loads(original['manifest.json']))
            inventories['linux']['packages'] = []
            with self.assertRaisesRegex(ValueError, 'all three'):
                unified.assemble(root, record, inventories, 'opendeck')

    def test_manager_only_package_is_rejected_before_opening_inputs(self):
        for capability in ('telemetry-v1', 'desktop-input-v1'):
            record = {'required_capabilities': [capability]}
            self.assertEqual(catalog.vendor_incompatibility(record)['status'], 'incompatible')
            with self.assertRaisesRegex(ValueError, 'required Manager capabilities'):
                unified.assemble(None, record, {}, 'opendeck')
        self.assertIsNone(catalog.vendor_incompatibility({'required_capabilities': []}))

    def test_retire_only_previously_generated_opendeck_zip(self):
        with tempfile.TemporaryDirectory() as folder:
            root, identity = Path(folder).resolve(), 'test.manager-only'
            path = unified.path_for(root, identity, 'opendeck')
            path.parent.mkdir(parents=True)
            path.write_bytes(b'previous export')
            package = {'id': identity, 'path': path.relative_to(root).as_posix(), 'sha256': catalog.digest(path)}
            unified.remember_package(path, 'fixture', package)
            path.write_bytes(b'different file')
            unified.retire_opendeck_bundle(root, identity)
            self.assertTrue(path.is_file())
            path.write_bytes(b'previous export')
            unified.retire_opendeck_bundle(root, identity)
            self.assertFalse(path.exists())
            self.assertFalse(path.with_suffix('.cache.json').exists())

    # Verify both hosts write level six deflate with permissions
    def test_both_hosts_write_level_six_deflate_with_permissions(self):
        payload = (b'plugin payload ' * 1024) + bytes(range(256))
        compressor = zlib.compressobj(6, zlib.DEFLATED, -15)
        expected = compressor.compress(payload) + compressor.flush()
        with tempfile.TemporaryDirectory() as folder:
            for host in ('manager', 'opendeck'):
                path = Path(folder) / (host + '.zip')
                unified.write_archive(path, 'test.counter', {'bin/plugin': payload}, {'bin/plugin'}, host)
                raw = path.read_bytes()
                with zipfile.ZipFile(path) as archive:
                    entry = archive.infolist()[0]
                    self.assertEqual(entry.compress_type, zipfile.ZIP_DEFLATED)
                    self.assertEqual(entry.external_attr >> 16 & 0o777, 0o755)
                    self.assertEqual(archive.read(entry), payload)
                    name_len, extra_len = struct.unpack_from('<HH', raw, entry.header_offset + 26)
                    offset = entry.header_offset + 30 + name_len + extra_len
                    self.assertEqual(raw[offset:offset + entry.compress_size], expected)

    # Verify wrapper rejects system specific opendeck
    def test_wrapper_rejects_system_specific_opendeck(self):
        for action in ('build', 'package', 'cross'):
            with self.assertRaisesRegex(ValueError, 'universal'):
                build.command(action, {}, 1, 'opendeck')
        self.assertEqual(build.command('unified', {}, 1, 'opendeck')[-2:], ['--host', 'opendeck'])

    # Verify wrapper defaults unified to both hosts only
    def test_wrapper_defaults_unified_to_both_hosts_only(self):
        self.assertEqual(build.command('unified', {}, 2)[-2:], ['--host', 'all'])
        self.assertEqual(build.command('unified', {}, 2, 'manager')[-2:], ['--host', 'manager'])
        for action in ('build', 'package', 'cross'):
            with self.assertRaisesRegex(ValueError, 'universal'):
                build.command(action, {}, 2, 'all')
        self.assertNotIn('--host', build.command('build', {}, 2))

    # Verify alias collision rejected
    def test_alias_collision_rejected(self):
        manifest = {'Icon': 'icon.png'}
        alias = 'openaction-assets/' + hashlib.sha256(b'icon.png').hexdigest() + '.png'
        files = {'icon.png': b'PNG', 'icon.svg': b'SVG', alias: b'tampered'}
        with self.assertRaisesRegex(ValueError, 'differs'):
            unified.opendeck_export.export_manifest(manifest, files, 'linux', materialize_aliases=True)
