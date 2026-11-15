package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// dependencyFixture creates a disposable module dependency fixture
func dependencyFixture(t *testing.T) (*buildRunner, func(string, string), *atomic.Int32) {
	t.Helper()
	builder := testRunner(t)
	builder.root = t.TempDir()
	builder.setenv("GOWORK", "off")
	builder.setenv("GOFLAGS", "")
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(builder.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/plugins\ngo 1.23\nrequire example.test/dependency v0.0.0\nreplace example.test/dependency => ./dependency\n")
	write("cmd/sample/main.go", "package main\nimport (\"embed\"; _ \"example.test/dependency\")\n//go:embed assets/*\nvar files embed.FS\nfunc main() {}\n")
	write("cmd/sample/assets/one.txt", "one")
	write("cmd/sample/excluded.go", "//go:build dependency_disabled\n\npackage main\nconst disabled = 1\n")
	write("dependency/go.mod", "module example.test/dependency\ngo 1.23\n")
	write("dependency/dependency.go", "package dependency\nconst Value = 1\n")
	write("dependency/LICENSE", "license one")
	calls := &atomic.Int32{}
	builder.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "go" && len(args) > 0 && args[0] == "list" {
			calls.Add(1)
		}
		return exec.CommandContext(ctx, name, args...)
	}
	return builder, write, calls
}

// TestSelectedDependencyClosureAndReuse verifies selected dependency closure and reuse
func TestSelectedDependencyClosureAndReuse(t *testing.T) {
	b, write, calls := dependencyFixture(t)
	analyze := func(refresh bool) *dependencyAnalysis {
		t.Helper()
		a, err := b.dependencies("sample", runtime.GOOS, runtime.GOARCH, refresh)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := analyze(true)
	if got := analyze(false); got != first || calls.Load() != 1 {
		t.Fatal("unchanged analysis not reused", calls.Load())
	}
	write("internal/unrelated/plugin.go", "package unrelated\nconst Value=2\n")
	write("cmd/sample/excluded.go", "//go:build dependency_disabled\n\npackage main\nconst disabled = 2\n")
	if a := analyze(true); a.fingerprint != first.fingerprint {
		t.Fatal("unselected source invalidates binary")
	}
	for _, change := range []struct{ name, body string }{
		{"dependency/dependency.go", "package dependency\nconst Value=2\n"},
		{"cmd/sample/assets/one.txt", "changed"},
		{"cmd/sample/assets/two.txt", "new embedded member"},
		{"cmd/sample/added.go", "package main\nconst Added=1\n"},
	} {
		before := analyze(false)
		count := calls.Load()
		write(change.name, change.body)
		after := analyze(false)
		if after.fingerprint == before.fingerprint || calls.Load() != count+1 {
			t.Fatalf("change not reanalyzed: %s", change.name)
		}
	}
	// Newly imported dependency must join both content and licensing closure
	write("dependency/extra/extra.go", "package extra\n")
	write("cmd/sample/added.go", "package main\nimport _ \"example.test/dependency/extra\"\n")
	after := analyze(false)
	found := false
	for _, p := range after.packages {
		if p.ImportPath == "example.test/dependency/extra" {
			found = true
		}
	}
	if !found {
		t.Fatal("new import missing")
	}
	write("cmd/sample/added.go", "package main\nimport _ \"example.test/missing\"\n")
	if _, err := b.dependencies("sample", runtime.GOOS, runtime.GOARCH, false); err == nil {
		t.Fatal("invalid dependencies reused stale analysis")
	}
}

// TestModuleNoticeCacheObservesLocalLicenseChanges verifies module notice cache observes local license changes
func TestModuleNoticeCacheObservesLocalLicenseChanges(t *testing.T) {
	b, write, _ := dependencyFixture(t)
	a, err := b.dependencies("sample", runtime.GOOS, runtime.GOARCH, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.moduleNotices(a.modules)
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.moduleNotices(a.modules)
	if err != nil || !bytes.Equal(first, again) || len(b.dependencyCache.notices) != 1 {
		t.Fatal("notice cache failed", err)
	}
	again[0] = '!'
	untouched, _ := b.moduleNotices(a.modules)
	if !bytes.Equal(first, untouched) {
		t.Fatal("caller mutated cached notices")
	}
	write("dependency/LICENSE", "license two")
	changed, err := b.moduleNotices(a.modules)
	if err != nil || bytes.Equal(first, changed) {
		t.Fatal("stale license", err)
	}
	if err := os.Remove(filepath.Join(b.root, "dependency/LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.moduleNotices(a.modules); err == nil {
		t.Fatal("missing license reused cached notices")
	}
	// The entire production notice path uses the same analysis, no second go list
	write("dependency/LICENSE", "license restored")
	if _, err := b.dependencyNotices(official.Definition{Slug: "sample"}, runtime.GOOS, []string{runtime.GOARCH}); err != nil {
		t.Fatal(err)
	}
}

// TestDependencyOverlayFailsClosed verifies dependency overlay fails closed
func TestDependencyOverlayFailsClosed(t *testing.T) {
	var configuration binaryBuildConfiguration
	configuration.Go = json.RawMessage(`{"GOFLAGS":"-overlay=/tmp/overlay.json"}`)
	if err := rejectDependencyOverlay(configuration); err == nil {
		t.Fatal("overlay could silently hash unmodified source")
	}
}

// Run with -run '^$' -bench BenchmarkDependencyAnalysis -benchtime=3x
// Measures real source closure analysis and subsequent package-stage reuse
func BenchmarkDependencyAnalysis(b *testing.B) {
	root, err := filepath.Abs("../..")
	if err != nil {
		b.Fatal(err)
	}
	for _, slug := range []string{"counter-tally", "mqtt", "weather"} {
		for _, refresh := range []bool{true, false} {
			mode := "reuse"
			if refresh {
				mode = "fresh"
			}
			b.Run(slug+"/"+mode, func(b *testing.B) {
				builder := &buildRunner{buildConfig: buildConfig{root: root, environment: os.Environ()}, ctx: context.Background(), commandContext: exec.CommandContext}
				if _, err := builder.dependencies(slug, runtime.GOOS, runtime.GOARCH, true); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := builder.dependencies(slug, runtime.GOOS, runtime.GOARCH, refresh); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
