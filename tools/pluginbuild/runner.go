package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// buildConfig is a per-invocation snapshot. Neither parsing nor plan validation
// changes process environment, working directory or package selection
type buildConfig struct {
	root, action, selected, targetOS, targetArch, planFile string
	macosIntel, force                                      bool
	jobs                                                   int
	environment                                            []string
}

type lockedBuildWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

// Write writes the captured output through the configured writer
func (writer lockedBuildWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(data)
}

type buildRunner struct {
	dependencyCache dependencyAnalysisCache
	buildConfig
	ctx               context.Context
	stdout, stderr    io.Writer
	sourceDefinitions []official.Definition
	planSelected      map[string]bool
	activePlan        *releasePlan
	// Tests can inject command preparation without changing package globals
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// environmentValue reads the effective environment override
func environmentValue(environment []string, key string) string {
	for i := len(environment) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(environment[i], key+"="); ok {
			return value
		}
	}
	return ""
}

// withEnvironment applies the selected environment overrides to a command
func withEnvironment(environment []string, values map[string]string) []string {
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := values[key]; !replace {
			result = append(result, entry)
		}
	}
	for _, key := range inputNames(values) {
		result = append(result, key+"="+values[key])
	}
	return result
}

// parseConfig parses and validates build runner configuration
func parseConfig(args []string, root string, environment []string, stderr io.Writer) (buildConfig, error) {
	config := buildConfig{root: root, environment: append([]string{}, environment...), jobs: min(4, runtime.NumCPU())}
	if len(args) == 0 {
		return config, fmt.Errorf("use catalog|assets|build|cross|package|check")
	}
	config.action = args[0]
	flags := flag.NewFlagSet(config.action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&config.planFile, "plan", "", "immutable release plan (build selected plugins only)")
	flags.StringVar(&config.selected, "plugin", "", "plugin slug")
	flags.BoolVar(&config.force, "force", false, "rebuild and repackage without cache")
	flags.IntVar(&config.jobs, "jobs", config.jobs, "maximum parallel plugin builds")
	flags.StringVar(&config.targetOS, "os", runtime.GOOS, "target OS")
	flags.StringVar(&config.targetArch, "arch", "", "package architecture (amd64, arm64, or explicit macOS universal)")
	flags.BoolVar(&config.macosIntel, "macos-intel", environmentValue(environment, "PLUGIN_MACOS_INTEL") == "1", "include optional Intel macOS builds (default: Apple Silicon only)")
	if err := flags.Parse(args[1:]); err != nil {
		return config, err
	}
	return config, nil
}

// newBuildRunner constructs the runner with its bounded worker settings
func newBuildRunner(ctx context.Context, config buildConfig, stdout, stderr io.Writer) (*buildRunner, error) {
	config.environment = append([]string{}, config.environment...)
	outputLock := &sync.Mutex{}
	stdout = lockedBuildWriter{outputLock, stdout}
	stderr = lockedBuildWriter{outputLock, stderr}
	runner := &buildRunner{buildConfig: config, ctx: ctx, stdout: stdout, stderr: stderr, sourceDefinitions: append([]official.Definition{}, official.Definitions...), commandContext: exec.CommandContext}
	if config.planFile != "" {
		if config.selected != "" {
			return nil, fmt.Errorf("--plugin cannot override an immutable release plan")
		}
		path := config.planFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(config.root, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		plan, selection, environment, err := validatePlan(data, runner.sourceDefinitions, config.environment)
		if err != nil {
			return nil, err
		}
		runner.activePlan, runner.planSelected, runner.environment = plan, selection, environment
	}
	if _, err := runner.definitions(); err != nil {
		return nil, err
	}
	return runner, nil
}

// newCommand constructs a command using the runner policy
func (builder *buildRunner) newCommand(name string, args ...string) *exec.Cmd {
	cmd := builder.commandContext(builder.ctx, name, args...)
	cmd.Dir = builder.root
	cmd.Env = append([]string{}, builder.environment...)
	return cmd
}
