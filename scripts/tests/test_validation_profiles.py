"""Regression checks for lightweight and full CI routing."""
from pathlib import Path
import unittest


WORKFLOWS = Path(__file__).resolve().parents[2] / '.github/workflows'


class ValidationProfilesTest(unittest.TestCase):
    # Require the manual default to skip packages without changing OS routing
    def test_development_quick_default_keeps_native_tests(self):
        workflow = (WORKFLOWS / 'development-checks.yml').read_text()
        self.assertIn('options: [quick, full]\n                default: quick', workflow)
        self.assertIn("if: inputs.profile == 'full'", workflow)
        self.assertIn('go test -vet=off ./...', workflow)
        self.assertIn('package --os "$EXPECTED_OS" --arch "$EXPECTED_ARCH"', workflow)

    # An exact SDK revision may share Go data only within the matching native target
    def test_development_go_cache_uses_exact_inputs(self):
        items = (WORKFLOWS / 'development-checks.yml').read_text().splitlines()
        key = next(line for line in items if 'key: plugins-dev-go-' in line)
        for token in ('matrix.os', 'env.EXPECTED_ARCH', '1.27.4', 'go.sum', 'inputs.sdk_commit'):
            self.assertIn(token, key)
        path = items[items.index(key) - 2:items.index(key)]
        self.assertEqual([line.strip() for line in path], ['${{ env.GOCACHE }}', '${{ env.GOMODCACHE }}'])

    # Pushes keep testing and packaging all operating systems without duplicate Linux jobs
    def test_test_build_remains_full_and_ci_remains_reusable(self):
        native = (WORKFLOWS / 'native-validation.yml').read_text()
        self.assertIn('branches: [test-build]', native)
        self.assertIn("github.event_name == 'push' && 'full' || inputs.profile", native)
        group = next(line for line in native.splitlines() if 'group: plugins-native-validation-' in line)
        self.assertIn('inputs.targets', group)
        self.assertIn('inputs.profile', group)
        self.assertIn("GOWORK: 'off'", native)
        for step in ('compile', 'packages', 'validation'):
            self.assertIn(f"id: {step}\n              if: env.CHECK_PROFILE == 'full'", native)
        self.assertIn("steps.validation.outcome == 'success'", native)
        checks = (WORKFLOWS / 'ci.yml').read_text()
        self.assertNotIn('test-build', checks)
        self.assertIn('workflow_call:', checks)
        self.assertIn("github.ref == 'refs/heads/release' && inputs.release_checks && inputs.commit == github.sha", checks)

    # Alternate runners are opt-in and native hardware evidence remains self-hosted
    def test_runner_service_is_opt_in_for_matching_jobs(self):
        for name in ('development-checks', 'prepare-release', 'publish-release'):
            with self.subTest(workflow=name):
                workflow = (WORKFLOWS / f'{name}.yml').read_text()
                self.assertIn('runner_service:', workflow)
                self.assertIn('options: [github, blacksmith]\n                default: github', workflow)
                line = next(line for line in workflow.splitlines() if 'runs-on:' in line)
                self.assertIn("inputs.runner_service == 'blacksmith'", line)
                self.assertIn('blacksmith-4vcpu-ubuntu-2404', line)
                self.assertIn('ubuntu-24.04', line)
                if name == 'development-checks':
                    self.assertIn('blacksmith-6vcpu-macos-15', line)
                    self.assertIn('blacksmith-4vcpu-windows-2025', line)
        native = (WORKFLOWS / 'native-validation.yml').read_text()
        self.assertNotIn('runner_service', native)
        self.assertIn("== 'self-hosted'", native)

    # Compile release binaries only in the packaging job and never drop an existing target
    def test_release_checks_do_not_repeat_packaging_compilation(self):
        checks = (WORKFLOWS / 'ci.yml').read_text()
        self.assertNotIn('./tools/pluginbuild cross', checks)
        self.assertIn('go vet ./...', checks)
        self.assertIn('go test -vet=off ./...', checks)
        packages = (WORKFLOWS / 'packages.yml').read_text()
        self.assertIn('for platform in linux windows darwin', packages)
        self.assertIn('python scripts/validate_packages.py --all-platforms', packages)
        self.assertIn("inputs.macos_intel && 'dist/packages/darwin/amd64/*.zip'", packages)
