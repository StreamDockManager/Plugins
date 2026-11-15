package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"testing"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// testRunner creates a runner backed by disposable paths
func testRunner(t *testing.T) *buildRunner {
	t.Helper()
	return &buildRunner{buildConfig: buildConfig{root: t.TempDir(), environment: os.Environ(), jobs: 2}, ctx: context.Background(), stdout: io.Discard, stderr: io.Discard, sourceDefinitions: append([]official.Definition{}, official.Definitions...), commandContext: exec.CommandContext}
}

// setenv sets the fixture environment override
func (builder *buildRunner) setenv(key, value string) {
	builder.environment = withEnvironment(builder.environment, map[string]string{key: value})
}

// readPlan reads the generated build plan for assertions
func (builder *buildRunner) readPlan() error {
	if builder.selected != "" {
		return fmt.Errorf("--plugin cannot override an immutable release plan")
	}
	data, err := os.ReadFile(builder.planFile)
	if err != nil {
		return err
	}
	plan, selection, environment, err := validatePlan(data, builder.sourceDefinitions, builder.environment)
	if err != nil {
		return err
	}
	builder.activePlan, builder.planSelected, builder.environment = plan, selection, environment
	return nil
}

// TestConfigSnapshotsEnvironmentAndFlagPrecedence verifies config snapshots environment and flag precedence
func TestConfigSnapshotsEnvironmentAndFlagPrecedence(t *testing.T) {
	env := []string{"PLUGIN_MACOS_INTEL=1", "UNCHANGED=value"}
	config, err := parseConfig([]string{"package", "--macos-intel=false", "--os=linux", "--arch=arm64", "--jobs=3"}, t.TempDir(), env, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	env[1] = "UNCHANGED=modified"
	if config.macosIntel || config.targetOS != "linux" || config.targetArch != "arm64" || config.jobs != 3 || environmentValue(config.environment, "UNCHANGED") != "value" {
		t.Fatalf("incorrect config snapshot: %+v", config)
	}
}

// TestIndependentRunnersAndCommands verifies independent runners and commands
func TestIndependentRunnersAndCommands(t *testing.T) {
	config, err := parseConfig([]string{"check"}, t.TempDir(), []string{"TEST_VALUE=original"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	first, err := newBuildRunner(context.Background(), config, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newBuildRunner(context.Background(), config, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	first.setenv("TEST_VALUE", "first")
	first.selected = first.sourceDefinitions[0].Slug
	first.planSelected = map[string]bool{"only-first": true}
	config.environment[0] = "TEST_VALUE=caller"
	if environmentValue(second.environment, "TEST_VALUE") != "original" || second.selected != "" || second.planSelected != nil {
		t.Fatal("runner state leaked")
	}
	cmd := first.newCommand("go", "version")
	if cmd.Dir != first.root || environmentValue(cmd.Env, "TEST_VALUE") != "first" {
		t.Fatal("command lost runner config")
	}
	cmd.Env[0] = "TEST_VALUE=command"
	if environmentValue(first.environment, "TEST_VALUE") != "first" {
		t.Fatal("command modified runner environment")
	}
}

// TestPlanValidationDoesNotMutateEnvironment verifies plan validation does not mutate environment
func TestPlanValidationDoesNotMutateEnvironment(t *testing.T) {
	plan, builder := releasePlanFixture(t)
	before := append([]string{}, builder.environment...)
	processBefore := os.Environ()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	parsed, selection, environment, err := validatePlan(data, builder.sourceDefinitions, builder.environment)
	if err != nil {
		t.Fatal(err)
	}
	if parsed == nil || len(selection) != 1 || environmentValue(environment, "RELEASE_COMMIT") != plan.Commit {
		t.Fatal("missing validated plan output")
	}
	if !reflect.DeepEqual(before, builder.environment) || !reflect.DeepEqual(processBefore, os.Environ()) {
		t.Fatal("plan validation mutated environment")
	}
	plan.SchemaVersion = 999
	data, _ = json.Marshal(plan)
	parsed, selection, environment, err = validatePlan(data, builder.sourceDefinitions, builder.environment)
	if err == nil || parsed != nil || selection != nil || environment != nil {
		t.Fatal("failed validation exposed partial state")
	}
}

// TestCancelledRunnerDoesNotStartCommand verifies cancelled runner does not start command
func TestCancelledRunnerDoesNotStartCommand(t *testing.T) {
	builder := testRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	builder.ctx = ctx
	var command *exec.Cmd
	builder.commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command = exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
		return command
	}
	if err := builder.command("unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if command.Process != nil {
		t.Fatal("cancelled command started a process")
	}
}

// TestIndependentRunnersPackageConcurrently verifies independent runners package concurrently
func TestIndependentRunnersPackageConcurrently(t *testing.T) {
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			builder := packageFixture(t)
			builder.setenv("RELEASE_BUILD", name)
			builder.selected = builder.sourceDefinitions[0].Slug
			if err := builder.packWithNotices("linux", nil); err != nil {
				t.Fatal(err)
			}
			if environmentValue(builder.environment, "RELEASE_BUILD") != name {
				t.Fatal("concurrent runner changed provenance")
			}
		})
	}
}
