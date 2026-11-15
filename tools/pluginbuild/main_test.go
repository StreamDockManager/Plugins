package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// Fixed notices keep archive tests independent of the compiler and module cache
func (builder *buildRunner) packWithNotices(osname string, notices []byte) error {
	return builder.packPackages(osname, func(official.Definition, []string) ([]byte, error) { return notices, nil })
}

type packagedPlugin struct {
	ID                   string            `json:"id"`
	Version              string            `json:"version"`
	Path                 string            `json:"path"`
	MinimumManager       string            `json:"minimum_manager"`
	RequiredCapabilities []string          `json:"required_capabilities"`
	Files                map[string]string `json:"files"`
}

type packagedCatalog struct {
	Version    string           `json:"version"`
	ReleaseTag string           `json:"release_tag"`
	Packages   []packagedPlugin `json:"packages"`
}

// archiveFiles reads the archive inventory for assertions
func archiveFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range r.File {
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = contents
	}
	return files
}

// packageFixture creates a disposable plugin package fixture
func packageFixture(t *testing.T) *buildRunner {
	builder := testRunner(t)
	t.Helper()
	builder.macosIntel = false
	builder.root, builder.selected = t.TempDir(), ""
	builder.sourceDefinitions = []official.Definition{
		{Slug: "counter-tally", Name: "Counter", Version: "1.2.3", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1"}, Actions: []string{"increment"}},
		{Slug: "timers-pomodoro", Name: "Timer", Version: "4.5.6", MinimumManager: "0.2.0", RequiredCapabilities: []string{"plugin-protocol-v1", "surface-metadata-v1"}, Actions: []string{"countdown"}},
	}
	for _, name := range []string{"RELEASE_TAG", "RELEASE_CHANNEL", "RELEASE_BUILD", "RELEASE_COMMIT"} {
		builder.setenv(name, "")
	}
	builder.setenv("RELEASE_REPOSITORY", "example/plugins")
	for _, name := range []string{"go.mod", "inspectors/inspector.html", "inspectors/inspector.js", "inspectors/inspector-features.js", "inspectors/inspector.css", "LICENSE", "THIRD_PARTY_NOTICES.md", "README.md", "examples/new-plugins.json"} {
		path := filepath.Join(builder.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range builder.sourceDefinitions {
		path := builder.binary("linux", "amd64", d.Slug)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture executable"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return builder
}

// TestIndividualPackagesOwnTheirVersionsAndInventories verifies individual packages own their versions and inventories
func TestIndividualPackagesOwnTheirVersionsAndInventories(t *testing.T) {
	builder := packageFixture(t)
	for index := range 2 {
		if index == 1 {
			builder.sourceDefinitions[0].Version = "1.2.4"
		}
		if err := builder.packWithNotices("linux", []byte("fixture license notices")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(builder.root, "dist", "catalog-linux.json"))
		if err != nil {
			t.Fatal(err)
		}
		var catalog packagedCatalog
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		if len(catalog.Packages) != len(builder.sourceDefinitions) {
			t.Fatal("missing packages")
		}
		for i, p := range catalog.Packages {
			d := builder.sourceDefinitions[i]
			if p.ID != official.ID(d.Slug) || p.Version != d.Version || p.MinimumManager != d.MinimumManager || !reflect.DeepEqual(p.RequiredCapabilities, d.RequiredCapabilities) {
				t.Fatal("package metadata differs", p)
			}
			zipBytes, err := os.ReadFile(filepath.Join(builder.root, "dist", "packages", "linux", "amd64", filepath.Base(p.Path)))
			if err != nil {
				t.Fatal(err)
			}
			files := archiveFiles(t, zipBytes)
			for name, expected := range p.Files {
				if hash(files[p.ID+".sdPlugin/"+name]) != expected {
					t.Fatal("inventory mismatch", name)
				}
			}
			var manifest struct{ Version string }
			if err := json.Unmarshal(files[p.ID+".sdPlugin/manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Version != d.Version {
				t.Fatal("manifest version mismatch")
			}
		}
		matches, _ := filepath.Glob(filepath.Join(builder.root, "dist", "plugin-bundle-*.zip"))
		if len(matches) != 0 {
			t.Fatal("unexpected bundle")
		}
	}
}

// TestSourceFingerprintIgnoresGeneratedReports verifies source fingerprint ignores generated reports
func TestSourceFingerprintIgnoresGeneratedReports(t *testing.T) {
	builder := packageFixture(t)
	packageCatalog := func() []byte {
		t.Helper()
		if err := builder.packWithNotices("linux", []byte("fixture license notices")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(builder.root, "dist", "catalog-linux.json"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := builder.fingerprint()
	catalog := packageCatalog()
	report := filepath.Join(builder.root, "artifacts", "measurement", "report.json")
	if err := os.MkdirAll(filepath.Dir(report), 0755); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`{"status":"running"}`, `{"status":"passed"}`} {
		if err := os.WriteFile(report, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if builder.fingerprint() != before || !bytes.Equal(packageCatalog(), catalog) {
			t.Fatal("generated reports changed the source fingerprint or package catalog")
		}
	}
	if err := os.WriteFile(filepath.Join(builder.root, "README.md"), []byte("changed source documentation"), 0644); err != nil {
		t.Fatal(err)
	}
	if builder.fingerprint() == before || bytes.Equal(packageCatalog(), catalog) {
		t.Fatal("source edits did not invalidate the fingerprint and package catalog")
	}
}

// TestPackagingRejectsInvalidPluginMetadata verifies packaging rejects invalid plugin metadata
func TestPackagingRejectsInvalidPluginMetadata(t *testing.T) {
	for _, value := range []string{"", "v1.0.0", "1.0", "01.0.0", "1.0.0-beta", "1.0.0+build"} {
		t.Run(value, func(t *testing.T) {
			builder := packageFixture(t)
			builder.sourceDefinitions[0].Version = value
			if err := builder.packWithNotices("linux", nil); err == nil || !strings.Contains(err.Error(), "plugin version") {
				t.Fatal("invalid plugin version was not rejected before packaging", err)
			}
		})
	}
}

// TestSpotifyPackageIncludesDistinctActionIconsAndStatusLayouts verifies spotify package includes distinct action icons and status layouts
func TestSpotifyPackageIncludesDistinctActionIconsAndStatusLayouts(t *testing.T) {
	builder := packageFixture(t)
	builder.sourceDefinitions[0].Slug = "spotify"
	builder.sourceDefinitions[0].Actions = []string{"toggle", "next", "previous", "volume", "seek", "status", "volume-display", "progress", "track-info"}
	if err := os.WriteFile(builder.binary("linux", "amd64", "spotify"), []byte("fixture executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := builder.packWithNotices("linux", nil); err != nil {
		t.Fatal(err)
	}
	id := official.ID("spotify")
	data, err := os.ReadFile(filepath.Join(builder.root, "dist", "packages", "linux", "amd64", id+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	files := archiveFiles(t, data)
	var manifest struct {
		Actions []struct {
			UUID, Icon  string
			Controllers []string
			States      []struct{ Image string }
		}
	}
	if err := json.Unmarshal(files[id+".sdPlugin/manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	for _, action := range manifest.Actions {
		suffix := strings.TrimPrefix(action.UUID, id+".")
		switch suffix {
		case "volume", "seek":
			if strings.Join(action.Controllers, ",") != "Knob" {
				t.Fatal("dial action exposed on other controllers", action)
			}
		case "volume-display", "progress", "track-info":
			if strings.Join(action.Controllers, ",") != "Keypad,Information" {
				t.Fatal("display controller mismatch", action)
			}
		}
		if action.Icon != "spotify/"+suffix+".svg" || len(files[id+".sdPlugin/"+action.Icon]) == 0 || len(files[id+".sdPlugin/"+official.Descriptor(builder.sourceDefinitions[0]).Actions[action.UUID].FeedbackLayout]) == 0 {
			t.Fatal("missing action icon or layout", action)
		}
		for _, state := range action.States {
			if len(files[id+".sdPlugin/"+state.Image]) == 0 {
				t.Fatal("missing state icon", state)
			}
		}
	}
	for name := range official.SpotifyAssets() {
		if len(files[id+".sdPlugin/"+name]) == 0 {
			t.Fatal("packaged runtime asset is missing", name)
		}
	}
	layout := files[id+".sdPlugin/spotify/status-wide.json"]
	if !bytes.Contains(layout, []byte(`"displayPalette":true`)) {
		t.Fatal("Spotify display layout does not declare its palette roles")
	}
	for name := range files {
		if strings.HasPrefix(name, id+".sdPlugin/palettes/") {
			t.Fatal("package still bundles palette variants", name)
		}
	}
}

// TestEveryPluginPackagesItsIconsStateImagesAndFeedbackLayouts verifies every plugin packages its icons state images and feedback layouts
func TestEveryPluginPackagesItsIconsStateImagesAndFeedbackLayouts(t *testing.T) {
	all := append([]official.Definition(nil), official.Definitions...)
	builder := packageFixture(t)
	builder.sourceDefinitions = all
	for _, d := range all {
		path := builder.binary("linux", "amd64", d.Slug)
		if err := os.WriteFile(path, []byte("fixture executable"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.packWithNotices("linux", nil); err != nil {
		t.Fatal(err)
	}
	catalogBytes, err := os.ReadFile(filepath.Join(builder.root, "dist", "catalog-linux.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog packagedCatalog
	if err := json.Unmarshal(catalogBytes, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, p := range catalog.Packages {
		t.Run(p.ID, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(builder.root, "dist", "packages", "linux", "amd64", p.ID+".zip"))
			if err != nil {
				t.Fatal(err)
			}
			files := archiveFiles(t, data)
			prefix := p.ID + ".sdPlugin/"
			var manifest struct {
				Icon, CategoryIcon string
				Actions            []struct {
					UUID, Icon string
					States     []struct{ Image string }
				}
			}
			if err := json.Unmarshal(files[prefix+"manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			checkIcon := func(icon string) {
				t.Helper()
				name := icon
				data := files[prefix+name]
				if len(data) == 0 || p.Files[name] != hash(data) {
					t.Fatal("missing or unhashed icon", name)
				}
			}
			checkIcon(manifest.Icon)
			checkIcon(manifest.CategoryIcon)
			for _, action := range manifest.Actions {
				checkIcon(action.Icon)
				if len(action.States) == 0 {
					t.Fatal("missing default state")
				}
				for _, state := range action.States {
					checkIcon(state.Image)
				}
				var definition official.Definition
				for _, d := range builder.sourceDefinitions {
					if official.ID(d.Slug) == p.ID {
						definition = d
					}
				}
				layout := official.Descriptor(definition).Actions[action.UUID].FeedbackLayout
				if data := files[prefix+layout]; len(data) == 0 || p.Files[layout] != hash(data) {
					t.Fatal("missing or unhashed Manager layout", layout)
				}
			}
		})
	}
}

// TestDeflateHeadersEmptyPayloadCRCAndExecutablePermissions verifies deflate headers empty payload crc and executable permissions
func TestDeflateHeadersEmptyPayloadCRCAndExecutablePermissions(t *testing.T) {
	for _, code := range [][]byte{nil, []byte("executable payload"), bytes.Repeat([]byte{0}, 8192)} {
		files := map[string][]byte{"plugin": code, "manifest.json": []byte("{}")}
		first, err := archive("test.sdPlugin/", files, "plugin")
		if err != nil {
			t.Fatal(err)
		}
		second, err := archive("test.sdPlugin/", files, "plugin")
		if err != nil || !bytes.Equal(first, second) {
			t.Fatal("non-deterministic archive", err)
		}
		decoded := archiveFiles(t, first)
		if !bytes.Equal(decoded["test.sdPlugin/plugin"], code) {
			t.Fatal("payload mismatch")
		}
		reader, _ := zip.NewReader(bytes.NewReader(first), int64(len(first)))
		for _, entry := range reader.File {
			if strings.HasSuffix(entry.Name, "/plugin") {
				if entry.Method != zip.Deflate || entry.Mode().Perm() != 0755 || entry.CRC32 != crc32.ChecksumIEEE(code) {
					t.Fatal("incorrect executable ZIP header", entry.FileHeader)
				}
			} else if entry.Method != zip.Deflate || entry.Mode().Perm() != 0644 {
				t.Fatal("supporting file policy changed")
			}
		}
	}
}

// TestMacPackagesDefaultToAppleSiliconAndIntelIsOptional verifies mac packages default to apple silicon and intel is optional
func TestMacPackagesDefaultToAppleSiliconAndIntelIsOptional(t *testing.T) {
	builder := packageFixture(t)
	for _, d := range builder.sourceDefinitions {
		for _, arch := range []string{"arm64"} {
			path := builder.binary("darwin", arch, d.Slug)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixture "+arch), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	builder.targetArch = ""
	if err := builder.packWithNotices("darwin", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(builder.root, "dist/catalog-macos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Packages []struct {
			ID, Path      string
			Architectures []string
		}
	}
	if err = json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Packages) != 2 {
		t.Fatal("default must package only Apple Silicon")
	}
	for _, p := range inventory.Packages {
		if len(p.Architectures) != 1 || p.Architectures[0] != "arm64" {
			t.Fatal("default package is not Apple Silicon")
		}
		arch := p.Architectures[0]
		if !strings.Contains(p.Path, "darwin/"+arch+"/") {
			t.Fatal(p.Path)
		}
		files := archiveFiles(t, mustRead(t, filepath.Join(builder.root, "dist", filepath.FromSlash(p.Path))))
		var manifest struct{ CodePath string }
		if err := json.Unmarshal(files[p.ID+".sdPlugin/manifest.json"], &manifest); err != nil {
			t.Fatal(err)
		}
		if string(files[p.ID+".sdPlugin/"+manifest.CodePath]) != "fixture "+arch {
			t.Fatal("wrong executable architecture")
		}
	}
	// No Intel binary existed for the default build. Explicit opt-in must add it
	for _, d := range builder.sourceDefinitions {
		path := builder.binary("darwin", "amd64", d.Slug)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture amd64"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	builder.macosIntel = true
	if err := builder.packWithNotices("darwin", nil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(builder.root, "dist/catalog-macos.json")), &inventory); err != nil || len(inventory.Packages) != 4 {
		t.Fatal("opt-in must include both Mac architectures", inventory, err)
	}
	builder.macosIntel = false
	builder.targetArch = "amd64"
	if err := builder.packWithNotices("darwin", nil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(builder.root, "dist/catalog-macos.json")), &inventory); err != nil || len(inventory.Packages) != 2 {
		t.Fatal(err, inventory)
	}
	for _, p := range inventory.Packages {
		if len(p.Architectures) != 1 || p.Architectures[0] != "amd64" {
			t.Fatal("explicit Intel selection ignored", p)
		}
	}
	for _, target := range [][2]string{{"linux", "arm64"}, {"windows", "universal"}, {"darwin", "riscv64"}} {
		if _, err := builder.packageVariants(target[0], target[1]); err == nil {
			t.Fatal("accepted unsupported target", target)
		}
	}
}

// TestCrossCompilationSkipsIntelUnlessRequested verifies cross compilation skips intel unless requested
func TestCrossCompilationSkipsIntelUnlessRequested(t *testing.T) {
	builder := testRunner(t)
	builder.macosIntel = false
	if !reflect.DeepEqual(builder.crossTargets(), [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"darwin", "arm64"}}) {
		t.Fatal(builder.crossTargets())
	}
	builder.macosIntel = true
	if !reflect.DeepEqual(builder.crossTargets(), [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"darwin", "amd64"}, {"darwin", "arm64"}}) {
		t.Fatal(builder.crossTargets())
	}
	if variants, err := builder.packageVariants("darwin", "universal"); err != nil || !reflect.DeepEqual(variants, []string{"universal"}) {
		t.Fatal(variants, err)
	}
}

// mustRead reads a fixture file or fails the test
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestPackagesKeepNamedExecutablesAndMatchingEntryPoints verifies packages keep named executables and matching entry points
func TestPackagesKeepNamedExecutablesAndMatchingEntryPoints(t *testing.T) {
	for _, target := range []struct{ os, arch, platformKey string }{
		{"linux", "amd64", "CodePathLinux"},
		{"windows", "amd64", "CodePathWin"},
		{"darwin", "arm64", "CodePathMac"},
		{"darwin", "amd64", "CodePathMac"},
	} {
		t.Run(target.os+"/"+target.arch, func(t *testing.T) {
			builder := packageFixture(t)
			builder.targetArch = target.arch
			for _, definition := range builder.sourceDefinitions {
				name := definition.Slug + "-plugin"
				if target.os == "windows" {
					name += ".exe"
				}
				path := builder.binary(target.os, target.arch, definition.Slug)
				if filepath.Base(path) != name {
					t.Fatalf("build filename = %s, want %s", path, name)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture "+definition.Slug), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := builder.packWithNotices(target.os, nil); err != nil {
				t.Fatal(err)
			}
			platform := target.os
			if platform == "darwin" {
				platform = "macos"
			}
			var catalog packagedCatalog
			if err := json.Unmarshal(mustRead(t, filepath.Join(builder.root, "dist", "catalog-"+platform+".json")), &catalog); err != nil {
				t.Fatal(err)
			}
			for _, p := range catalog.Packages {
				data := mustRead(t, filepath.Join(builder.root, "dist", filepath.FromSlash(p.Path)))
				files := archiveFiles(t, data)
				prefix := p.ID + ".sdPlugin/"
				var manifest map[string]any
				if err := json.Unmarshal(files[prefix+"manifest.json"], &manifest); err != nil {
					t.Fatal(err)
				}
				var slug string
				for _, d := range builder.sourceDefinitions {
					if official.ID(d.Slug) == p.ID {
						slug = d.Slug
					}
				}
				name := slug + "-plugin"
				if target.os == "windows" {
					name += ".exe"
				}
				if manifest["CodePath"] != name || manifest[target.platformKey] != name {
					t.Fatalf("entry points do not identify %s: %v", name, manifest)
				}
				if string(files[prefix+name]) != "fixture "+slug || p.Files[name] != hash(files[prefix+name]) {
					t.Fatal("named binary or inventory hash mismatch")
				}
				for _, legacy := range []string{"plugin", "plugin.exe"} {
					if _, ok := files[prefix+legacy]; ok {
						t.Fatal("generic executable retained", legacy)
					}
				}
				archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range archive.File {
					if entry.Name == prefix+name && (entry.Method != zip.Deflate || entry.Mode().Perm()&0111 != 0111) {
						t.Fatal("named binary lost compression or executable permissions")
					}
				}
			}
		})
	}
}

// TestInspectorAssemblyMatchesPackagesAndOwnsBytes verifies inspector assembly matches packages and owns bytes
func TestInspectorAssemblyMatchesPackagesAndOwnsBytes(t *testing.T) {
	builder := packageFixture(t)
	first, err := builder.inspectorAssets(builder.sourceDefinitions[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.inspectorAssets(builder.sourceDefinitions[0])
	if err != nil {
		t.Fatal(err)
	}
	first["inspector.js"][0] ^= 1
	if bytes.Equal(first["inspector.js"], second["inspector.js"]) {
		t.Fatal("shared mutable assembly")
	}
	if _, ok := second["descriptor.json"]; ok {
		t.Fatal("staging descriptor leaked into common assets")
	}
	if !bytes.Contains(second["settings-module.json"], []byte("\n    ")) {
		t.Fatal("settings formatting not normalized")
	}
	if err := builder.packWithNotices("linux", []byte("notices")); err != nil {
		t.Fatal(err)
	}
	d := builder.sourceDefinitions[0]
	archive := archiveFiles(t, mustRead(t, filepath.Join(builder.root, "dist/packages/linux/amd64", official.ID(d.Slug)+".zip")))
	for name, want := range second {
		if !bytes.Equal(archive[official.ID(d.Slug)+".sdPlugin/"+name], want) {
			t.Fatalf("staged/package bytes differ: %s", name)
		}
	}
	if err := os.Remove(filepath.Join(builder.root, "inspectors/inspector.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.inspectorAssets(d); err == nil {
		t.Fatal("missing inspector source accepted")
	}
}
