package main

import (
	"context"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeNoticesIncludeLocalReplacementsAndExcludeUnusedModules verifies runtime notices include local replacements and exclude unused modules
func TestRuntimeNoticesIncludeLocalReplacementsAndExcludeUnusedModules(t *testing.T) {
	builder := testRunner(t)
	builder.setenv("GOWORK", "off")
	// Every dependency below is a local synthetic module. Never resolve the
	// example.org fixture identities over the network, even on failure
	builder.setenv("GOPROXY", "off")
	builder.setenv("GOSUMDB", "off")
	builder.root = t.TempDir()
	canonical, err := filepath.EvalSymlinks(builder.root)
	if err != nil {
		t.Fatal(err)
	}
	builder.root = canonical
	builder.ctx = context.Background()
	files := map[string]string{
		"go.mod":              "module example.org/plugin\ngo 1.27.4\nrequire example.org/used v0.0.0\nrequire example.org/unused v0.0.0\nreplace example.org/used => ./used\nreplace example.org/unused => ./unused\n",
		"cmd/fixture/main.go": "package main\nimport \"example.org/used\"\nfunc main(){used.Run()}\n",
		"used/go.mod":         "module example.org/used\ngo 1.27.4\n", "used/used.go": "package used\nfunc Run(){}\n",
		"used/LICENSE": "REPLACEMENT LICENSE", "used/NOTICE": "REQUIRED NOTICE",
		"unused/go.mod": "module example.org/unused\ngo 1.27.4\n", "unused/unused.go": "package unused\n",
	}
	for name, data := range files {
		path := filepath.Join(builder.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := builder.dependencyNotices(official.Definition{Slug: "fixture"}, "linux", []string{"amd64"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	for _, marker := range []string{"example.org/used", "REPLACEMENT LICENSE", "REQUIRED NOTICE", "Go runtime and standard library"} {
		if !strings.Contains(text, marker) {
			t.Fatal("missing notice", marker)
		}
	}
	if strings.Contains(text, "unused") {
		t.Fatal("unused dependency shipped")
	}
	// Workspace dependencies are main modules too; only the executable's own
	// module is covered by the separately packaged project license
	if err := os.WriteFile(filepath.Join(builder.root, "go.mod"), []byte("module example.org/plugin\ngo 1.27.4\nrequire example.org/used v0.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(builder.root, "go.work")
	if err := os.WriteFile(work, []byte("go 1.27.4\nuse (\n.\n./used\n)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	builder.setenv("GOWORK", work)
	workspace, err := builder.dependencyNotices(official.Definition{Slug: "fixture"}, "linux", []string{"amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workspace), "REPLACEMENT LICENSE") {
		t.Fatal("workspace runtime module omitted")
	}
	if err := os.Remove(filepath.Join(builder.root, "used/LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.dependencyNotices(official.Definition{Slug: "fixture"}, "linux", []string{"amd64"}); err == nil {
		t.Fatal("missing required license accepted")
	}
}
