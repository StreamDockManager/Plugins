package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// TestArtifactCacheRejectsTamperingAndChangedInputs verifies artifact cache rejects tampering and changed inputs
func TestArtifactCacheRejectsTamperingAndChangedInputs(t *testing.T) {
	builder := testRunner(t)
	path := filepath.Join(t.TempDir(), "plugin")
	os.WriteFile(path, []byte("one"), 0755)
	if err := remember(path, "key"); err != nil {
		t.Fatal(err)
	}
	if !builder.cached(path, "key") || builder.cached(path, "other") {
		t.Fatal("wrong cache eligibility")
	}
	os.WriteFile(path, []byte("tampered"), 0755)
	if builder.cached(path, "key") {
		t.Fatal("tampered artifact trusted")
	}
}

// TestBinaryCacheTracksEffectiveBuildSettings verifies binary cache tracks effective build settings
func TestBinaryCacheTracksEffectiveBuildSettings(t *testing.T) {
	builder := testRunner(t)
	builder.root, builder.selected, builder.ctx = t.TempDir(), "counter-tally", context.Background()
	builder.sourceDefinitions = []official.Definition{{Slug: builder.selected, Name: "Counter", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1"}, Actions: []string{"increment"}}}
	frame, err := sdk.FrameCapabilities(official.Descriptor(builder.sourceDefinitions[0]))
	if err != nil {
		t.Fatal(err)
	}
	builder.setenv("GOWORK", "off")
	builder.setenv("GOFLAGS", "")
	for _, dir := range []string{"sdk", "internal", "cmd/counter-tally"} {
		if err := os.MkdirAll(filepath.Join(builder.root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{
		"go.mod":                       "module example.test/cache\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\n\nrequire github.com/StreamDockManager/Plugin-SDK/Go v0.1.0\nreplace github.com/StreamDockManager/Plugin-SDK/Go => ./sdk\n",
		"go.sum":                       "",
		"sdk/go.mod":                   "module github.com/StreamDockManager/Plugin-SDK/Go\n\ngo 1.27.4\n",
		"sdk/descriptor.go":            "package sdk\nvar CompiledDescriptor string\n",
		"cmd/counter-tally/main.go":    "package main\nimport (\"fmt\"; \"github.com/StreamDockManager/Plugin-SDK/Go\")\nfunc main() { fmt.Print(label + \"|\" + sdk.CompiledDescriptor) }\n",
		"cmd/counter-tally/default.go": "//go:build !cachevariant\n\npackage main\nconst label = \"default\"\n",
		"cmd/counter-tally/variant.go": "//go:build cachevariant\n\npackage main\nconst label = \"variant\"\n",
	} {
		if err := os.WriteFile(filepath.Join(builder.root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	buildAndRun := func(want string) {
		t.Helper()
		if err := builder.compile(runtime.GOOS, runtime.GOARCH); err != nil {
			t.Fatal(err)
		}
		data, err := exec.Command(builder.binary(runtime.GOOS, runtime.GOARCH, builder.selected)).Output()
		if err != nil || string(data) != want+"|"+frame {
			t.Fatalf("built %q, want label %q with injected descriptor: %v", data, want, err)
		}
	}
	buildAndRun("default")

	// Unimported implementations must not invalidate this plugin binary
	for _, name := range []string{"internal/plugins/counter-tally/feature.go", "internal/httpclient/cache.go"} {
		file := filepath.Join(builder.root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		configuration, err := builder.binaryConfiguration(builder.binaryEnvironment(runtime.GOOS, runtime.GOARCH))
		if err != nil {
			t.Fatal(err)
		}
		before, err := builder.binaryKey(builder.sourceDefinitions[0], runtime.GOOS, runtime.GOARCH, configuration)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("package fixture\n"), 0644); err != nil {
			t.Fatal(err)
		}
		after, err := builder.binaryKey(builder.sourceDefinitions[0], runtime.GOOS, runtime.GOARCH, configuration)
		if err != nil || before != after {
			t.Fatal("unrelated source invalidated binary", name, err)
		}
	}

	// The sources and target are unchanged. Only the effective Go build tags differ
	builder.setenv("GOFLAGS", "-tags=cachevariant")
	buildAndRun("variant")
	buildAndRun("variant")
	builder.setenv("GOFLAGS", "")
	buildAndRun("default")
	// A fork can change its module path. The linker must inject into the owning
	// SDK symbol rather than silently retaining the original repository's path
	for _, name := range []string{"go.mod", "cmd/counter-tally/main.go"} {
		data, err := os.ReadFile(filepath.Join(builder.root, name))
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.ReplaceAll(data, []byte("example.test/cache"), []byte("example.test/fork/plugins"))
		if err := os.WriteFile(filepath.Join(builder.root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	buildAndRun("default")
	// go list can report several main modules in a workspace. The SDK symbol
	// must come from this checkout's go.mod rather than the first listed sibling
	workspace := t.TempDir()
	other := filepath.Join(workspace, "other")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.test/aaa\n\ngo "+strings.TrimPrefix(runtime.Version(), "go")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workfile := filepath.Join(workspace, "go.work")
	moduleRoot, err := filepath.EvalSymlinks(builder.root)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err := filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	// Use the same path spelling for the compiler's cwd and go.work. On Windows
	// TempDir may use RUNNER~1 while EvalSymlinks expands it to runneradmin
	builder.root = moduleRoot
	contents := "go " + strings.TrimPrefix(runtime.Version(), "go") + "\nuse (\n" + strconv.Quote(moduleRoot) + "\n" + strconv.Quote(otherRoot) + "\n)\n"
	if err := os.WriteFile(workfile, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	builder.setenv("GOWORK", workfile)
	buildAndRun("default")
}

// TestPackageCacheTracksAssetsSeparately verifies package cache tracks assets separately
func TestPackageCacheTracksAssetsSeparately(t *testing.T) {
	builder := testRunner(t)
	path := filepath.Join(t.TempDir(), "plugin.zip")
	files := memoryFiles(map[string][]byte{"plugin": []byte("binary"), "icon.svg": []byte("first")})
	first, err := builder.archiveCached(path, "plugin/", files, "plugin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.archiveCached(path, "plugin/", files, "plugin")
	if err != nil || first.SHA256 != second.SHA256 {
		t.Fatal("cache hit changed bytes")
	}
	files["icon.svg"] = packageFile{data: []byte("second")}
	third, err := builder.archiveCached(path, "plugin/", files, "plugin")
	if err != nil || first.SHA256 == third.SHA256 {
		t.Fatal("changed asset reused old package")
	}
	files["plugin"] = packageFile{data: []byte("rebuilt executable")}
	fourth, err := builder.archiveCached(path, "plugin/", files, "plugin")
	if err != nil || third.SHA256 == fourth.SHA256 {
		t.Fatal("changed executable reused old package")
	}
}

// TestBinaryCacheTracksEmbeddedVCSAndModuleVersion verifies binary cache tracks embedded vcs and module version
func TestBinaryCacheTracksEmbeddedVCSAndModuleVersion(t *testing.T) {
	builder := testRunner(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for the VCS cache regression:", err)
	}
	builder.root, builder.selected, builder.ctx, builder.force = t.TempDir(), "counter-tally", context.Background(), false
	builder.sourceDefinitions = []official.Definition{{Slug: builder.selected, Name: "Counter", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1"}, Actions: []string{"increment"}}}
	builder.setenv("GOWORK", "off")
	builder.setenv("GOFLAGS", "")
	builder.setenv("GOPROXY", "off")
	builder.setenv("GOSUMDB", "off")
	builder.setenv("GOMODCACHE", t.TempDir())
	builder.setenv("GIT_CONFIG_NOSYSTEM", "1")
	builder.setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	for _, directory := range []string{"sdk", "internal", "cmd/counter-tally"} {
		if err := os.MkdirAll(filepath.Join(builder.root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{
		"go.mod":                    "module example.test/cache\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\n",
		"go.sum":                    "",
		".gitignore":                "/dist/\n",
		"README.md":                 "Fixture\n",
		"cmd/counter-tally/main.go": "package main\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(builder.root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(arguments ...string) string {
		t.Helper()
		c := exec.Command("git", arguments...)
		c.Dir = builder.root
		c.Env = builder.environment
		output, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--template="+t.TempDir())
	git("config", "user.name", "Cache fixture")
	git("config", "user.email", "cache@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	git("config", "core.autocrlf", "false")
	git("add", ".")
	git("commit", "-m", "Initial fixture")
	path := builder.binary(runtime.GOOS, runtime.GOARCH, builder.selected)
	build := func() ([]byte, *debug.BuildInfo) {
		t.Helper()
		if err := builder.compile(runtime.GOOS, runtime.GOARCH); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := buildinfo.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data, info
	}
	assertStamp := func(info *debug.BuildInfo, revision, modified string) {
		t.Helper()
		settings := map[string]string{}
		for _, setting := range info.Settings {
			settings[setting.Key] = setting.Value
		}
		if settings["vcs.revision"] != revision || settings["vcs.modified"] != modified {
			t.Fatalf("unexpected embedded VCS metadata: %+v", settings)
		}
	}
	first, info := build()
	assertStamp(info, git("rev-parse", "HEAD"), "false")
	// No plugin input changes, but the commit embedded by Go must change
	git("commit", "--allow-empty", "-m", "New revision")
	committed, info := build()
	assertStamp(info, git("rev-parse", "HEAD"), "false")
	if bytes.Equal(first, committed) {
		t.Fatal("new revision reused old binary")
	}
	if err := os.WriteFile(filepath.Join(builder.root, "README.md"), []byte("Changed documentation\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dirty, info := build()
	assertStamp(info, git("rev-parse", "HEAD"), "true")
	if bytes.Equal(committed, dirty) || !strings.HasSuffix(info.Main.Version, "+dirty") {
		t.Fatalf("dirty metadata was not rebuilt: %+v", info.Main)
	}
	if err := os.WriteFile(filepath.Join(builder.root, "README.md"), []byte("Fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	clean, info := build()
	assertStamp(info, git("rev-parse", "HEAD"), "false")
	if !bytes.Equal(committed, clean) {
		t.Fatal("restoring the same clean source changed binary bytes")
	}
	// Tags change the embedded module version even with identical HEAD and files
	git("tag", "-a", "v1.2.3", "-m", "Version fixture")
	// Go caches revision-to-version lookups separately. Give this simulated tag
	// checkout a fresh module cache so Go observes its newly published version
	builder.setenv("GOMODCACHE", t.TempDir())
	tagged, info := build()
	if info.Main.Version != "v1.2.3" || bytes.Equal(clean, tagged) {
		t.Fatalf("module version was not rebuilt: %+v", info.Main)
	}
	sentinel := time.Unix(1234567890, 0)
	if err := os.Chtimes(path, sentinel, sentinel); err != nil {
		t.Fatal(err)
	}
	cachedBytes, _ := build()
	stat, err := os.Stat(path)
	if err != nil || !stat.ModTime().Equal(sentinel) || !bytes.Equal(tagged, cachedBytes) {
		t.Fatal("same source did not reuse its verified binary cache", err)
	}
	builder.force = true
	forced, _ := build()
	if !bytes.Equal(cachedBytes, forced) {
		t.Fatal("forced and cached binaries differ for the same source and metadata")
	}
	builder.force = false
	git("tag", "-d", "v1.2.3")
	builder.setenv("GOMODCACHE", t.TempDir())
	untagged, info := build()
	if info.Main.Version == "v1.2.3" || !bytes.Equal(clean, untagged) {
		t.Fatalf("removed module version was not rebuilt: %+v", info.Main)
	}
}

// TestSDKCacheIncludesEffectiveLocalDependencyContents verifies SDK cache includes effective local dependency contents
func TestSDKCacheIncludesEffectiveLocalDependencyContents(t *testing.T) {
	builder := testRunner(t)
	builder.root, builder.ctx = t.TempDir(), context.Background()
	builder.setenv("GOWORK", "off")
	builder.setenv("GOPROXY", "off")
	dependency := filepath.Join(t.TempDir(), "sdk")
	if err := os.MkdirAll(dependency, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(builder.root, "go.mod"):  "module example.test/consumer\ngo 1.27.4\nrequire " + sdkModulePath + " v0.1.0\nreplace " + sdkModulePath + " => " + strconv.Quote(filepath.ToSlash(dependency)) + "\n",
		filepath.Join(dependency, "go.mod"):    "module " + sdkModulePath + "\ngo 1.27.4\n",
		filepath.Join(dependency, "client.go"): "package sdk\nvar CompiledDescriptor string\n",
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	before, err := builder.sdkConfiguration(builder.environment)
	if err != nil {
		t.Fatal(err)
	}
	originalFingerprint := builder.fingerprint()
	if err := os.WriteFile(filepath.Join(dependency, "client.go"), []byte("package sdk\nvar CompiledDescriptor string\nconst Feature=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := builder.sdkConfiguration(builder.environment)
	if err != nil {
		t.Fatal(err)
	}
	if before.ImportPath != sdkModulePath || before.Content == after.Content || originalFingerprint == builder.fingerprint() {
		t.Fatal("local SDK change did not invalidate cached source identity")
	}
	sourceFingerprint := builder.fingerprint()
	if err := os.WriteFile(filepath.Join(dependency, "inspector.js"), []byte("window.SDKSchema = 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	embedded, err := builder.sdkConfiguration(builder.environment)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Content == after.Content || sourceFingerprint == builder.fingerprint() {
		t.Fatal("embedded SDK inspector did not invalidate cache")
	}

}
