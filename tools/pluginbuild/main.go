// pluginbuild is the standalone build/package entry point; no Rust or Node required
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/inspectors"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// command runs a build command with the configured environment
func (builder *buildRunner) command(name string, args ...string) error {
	c := builder.newCommand(name, args...)
	c.Dir = builder.root
	c.Stdout = builder.stdout
	c.Stderr = builder.stderr
	return c.Run()
}

// hash hashes the input bytes for artifact identity
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// writeJSON writes the structured metadata in its stable JSON form
func writeJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "    ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

// definitions loads the registered plugin definitions
func (builder *buildRunner) definitions() ([]official.Definition, error) {
	if err := official.ValidateDefinitions(builder.sourceDefinitions); err != nil {
		return nil, err
	}
	if builder.selected == "" {
		if builder.planSelected != nil {
			ds := []official.Definition{}
			for _, d := range builder.sourceDefinitions {
				if builder.planSelected[official.ID(d.Slug)] {
					ds = append(ds, d)
				}
			}
			return ds, nil
		}
		return builder.sourceDefinitions, nil
	}
	for _, d := range builder.sourceDefinitions {
		if d.Slug == builder.selected {
			return []official.Definition{d}, nil
		}
	}
	return nil, fmt.Errorf("unknown PLUGIN %q", builder.selected)
}

// definitionsFor selects definitions matching the requested plugin identifiers
func (builder *buildRunner) definitionsFor(osname string) ([]official.Definition, error) {
	definitions, err := builder.definitions()
	if err != nil {
		return nil, err
	}
	result := []official.Definition{}
	for _, d := range definitions {
		if official.Supports(d.Slug, osname) {
			result = append(result, d)
		} else if builder.selected != "" {
			return nil, fmt.Errorf("%s does not support %s", d.Slug, osname)
		}
	}
	return result, nil
}

// executableName chooses the platform-specific executable filename
func executableName(osname, slug string) string {
	ext := ""
	if osname == "windows" {
		ext = ".exe"
	}
	return slug + "-plugin" + ext
}

// binary locates or builds the executable for the requested target
func (builder *buildRunner) binary(osname, arch, slug string) string {
	return filepath.Join(builder.root, "dist", "bin", osname, arch, executableName(osname, slug))
}

// binaryEnvironment constructs the target-specific compiler environment
func (builder *buildRunner) binaryEnvironment(osname, arch string) []string {
	env := append(append([]string{}, builder.environment...), "CGO_ENABLED=0", "GOMAXPROCS=1", "GOOS="+osname, "GOARCH="+arch)
	return env
}

// binaryArguments constructs the target-specific compiler arguments
func binaryArguments(osname, sdkImportPath, frame string) []string {
	flags := "-s -w"
	if osname == "windows" {
		// Plugins are background processes; inherited pipes still carry logs
		flags += " -H=windowsgui"
	}
	return []string{"build", "-p=1", "-trimpath", "-ldflags=" + flags + " -X " + sdkImportPath + ".CompiledDescriptor=" + frame}
}

// compile compiles the selected plugin for one native target
func (builder *buildRunner) compile(osname, arch string) error {
	ds, e := builder.definitionsFor(osname)
	if e != nil {
		return e
	}
	if len(ds) == 0 {
		return nil
	}
	environment := builder.binaryEnvironment(osname, arch)
	configuration, e := builder.binaryConfiguration(environment)
	if e != nil {
		return e
	}
	build, e := builder.prepareWindowsMemoryBuild(osname, environment, &configuration)
	if e != nil {
		return e
	}
	return builder.parallelDefinitions(ds, func(d official.Definition) error {
		key, err := builder.binaryKey(d, osname, arch, configuration)
		if err != nil {
			return err
		}
		path := builder.binary(osname, arch, d.Slug)
		if builder.cached(path, key) {
			fmt.Fprintf(builder.stderr, "Cached %s %s/%s\n", d.Slug, osname, arch)
			return nil
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		frame, err := sdk.FrameCapabilities(official.Descriptor(d))
		if err != nil {
			return err
		}
		arguments := append(binaryArguments(osname, configuration.SDK.ImportPath, frame), build.arguments...)
		c := builder.newCommand(build.command, append(arguments, "-o", path, "./cmd/"+d.Slug)...)
		c.Dir = builder.root
		c.Env = build.environment
		c.Stdout = builder.stdout
		c.Stderr = builder.stderr
		if err = c.Run(); err != nil {
			return err
		}
		return remember(path, key)
	})
}

// inspectorAssets assembles owned bytes in product, settings, SDK, profile, source
// order. Staging and packages deliberately share indented settings JSON
func (builder *buildRunner) inspectorAssets(d official.Definition) (map[string][]byte, error) {
	product := official.PluginAssets(d)
	if err := official.ValidateAssets(d, product); err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(product))
	copyAssets := func(assets map[string][]byte) {
		for name, data := range assets {
			files[name] = append([]byte(nil), data...)
		}
	}
	copyAssets(product)
	module, err := json.MarshalIndent(official.SettingsModule(d), "", "    ")
	if err != nil {
		return nil, err
	}
	files["settings-module.json"] = module
	copyAssets(inspectors.Assets())
	profiles, err := official.SuggestedProfiles(d)
	if err != nil {
		return nil, err
	}
	copyAssets(profiles)
	for _, name := range []string{"inspector.html", "inspector.js", "inspector-features.js", "inspector.css"} {
		data, err := os.ReadFile(filepath.Join(builder.root, "inspectors", name))
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	return files, nil
}

// pack packages the selected plugin and its verified inputs
func (builder *buildRunner) pack(osname string) error {
	return builder.packPackages(osname, func(d official.Definition, archs []string) ([]byte, error) {
		return builder.dependencyNotices(d, osname, archs)
	})
}

// universalTool locates the tool used to build universal packages
func universalTool(host string, lookup func(string) (string, error)) (string, error) {
	name := "llvm-lipo"
	if host == "darwin" {
		name = "lipo"
	} else if host != "linux" {
		return "", fmt.Errorf("universal macOS packaging requires Linux with llvm-lipo or macOS with lipo")
	}
	path, err := lookup(name)
	if err != nil {
		return "", fmt.Errorf("universal macOS packaging requires %s on PATH: %w", name, err)
	}
	return path, nil
}

// packageVariants selects the requested host and platform package variants
func (builder *buildRunner) packageVariants(osname, arch string) ([]string, error) {
	if osname == "darwin" {
		switch arch {
		case "":
			if builder.macosIntel {
				return []string{"amd64", "arm64"}, nil
			}
			return []string{"arm64"}, nil
		case "amd64", "arm64", "universal":
			return []string{arch}, nil
		}
	}
	if (osname == "windows" || osname == "linux") && (arch == "" || arch == "amd64") {
		return []string{"amd64"}, nil
	}
	return nil, fmt.Errorf("unsupported package target %s/%s", osname, arch)
}

// Workers record failures by input index, never completion order
func (builder *buildRunner) parallelPackages(ds []official.Definition, fn func(official.Definition) error) error {
	indexes := make(map[string]int, len(ds))
	failures := make([]error, len(ds))
	for i, d := range ds {
		indexes[d.Slug] = i
	}
	// Store each worker result in its stable input slot before selecting the first failure
	if err := builder.parallelDefinitions(ds, func(d official.Definition) error {
		failures[indexes[d.Slug]] = fn(d)
		return nil
	}); err != nil {
		return err
	}
	for i, err := range failures {
		if err != nil {
			return fmt.Errorf("%s: %w", ds[i].Slug, err)
		}
	}
	return nil
}

// packPackages builds the selected package inventory
func (builder *buildRunner) packPackages(osname string, noticeFor func(official.Definition, []string) ([]byte, error)) error {
	ds, e := builder.definitionsFor(osname)
	if e != nil {
		return e
	}
	variants, e := builder.packageVariants(osname, builder.targetArch)
	if e != nil {
		return e
	}
	out := filepath.Join(builder.root, "dist", "packages", osname)
	if e = os.MkdirAll(out, 0755); e != nil {
		return e
	}
	// Every worker owns one plugin's paths and result slot. Publish catalogs only
	// after all bounded work succeeds, preserving source order regardless of timing
	results := make([][]map[string]any, len(ds))
	indexes := make(map[string]int, len(ds))
	for i, d := range ds {
		indexes[d.Slug] = i
	}
	if err := builder.parallelPackages(ds, func(d official.Definition) error {
		var e error
		packages := []map[string]any{}
		if builder.ctx != nil && builder.ctx.Err() != nil {
			return builder.ctx.Err()
		}
		for _, variant := range variants {
			archs := []string{variant}
			arch := variant
			packageOut := filepath.Join(out, variant)
			if e = os.MkdirAll(packageOut, 0755); e != nil {
				return e
			}
			if variant == "universal" {
				archs = []string{"amd64", "arm64"}
				arch = "amd64"
			}
			notices, e := noticeFor(d, archs)
			if e != nil {
				return e
			}
			entry := executableName(osname, d.Slug)
			path := builder.binary(osname, arch, d.Slug)
			if variant == "universal" {
				tool, err := universalTool(runtime.GOOS, exec.LookPath)
				if err != nil {
					return err
				}
				path = filepath.Join(packageOut, d.Slug+"-universal")
				if e = builder.command(tool, "-create", builder.binary("darwin", "amd64", d.Slug), builder.binary("darwin", "arm64", d.Slug), "-output", path); e != nil {
					return e
				}
				if runtime.GOOS == "darwin" {
					// Newer native lipo versions reject multiple -verify_arch arguments
					for _, architecture := range []string{"x86_64", "arm64"} {
						if e = builder.command(tool, path, "-verify_arch", architecture); e != nil {
							return e
						}
					}
				} else if e = builder.command(tool, path, "-verify_arch", "x86_64", "arm64"); e != nil {
					return e
				}
			}
			actions := []map[string]any{}
			for _, a := range d.Actions {
				name := official.ActionName(d.Slug, a)
				if d.Slug == "microphone-controls" {
					name = map[string]string{"mute": "Mute microphone", "unmute": "Unmute microphone", "toggle": "Toggle microphone mute"}[a]
				}
				visual := official.VisualFor(d.Slug, a)
				icon := visual.Icon + ".svg"
				states := []map[string]any{}
				for _, image := range visual.States {
					states = append(states, map[string]any{"Image": image + ".svg"})
				}
				item := map[string]any{"UUID": official.ID(d.Slug) + "." + a, "Name": name, "Controllers": []string{"Keypad", "Knob"}, "PropertyInspectorPath": "inspector.html", "Icon": icon, "States": states}
				types := official.Descriptor(d).Actions[official.ID(d.Slug)+"."+a].ControlTypes
				if strings.Join(types, ",") != "button,encoder,touch" {
					controllers := []string{}
					for _, kind := range types {
						controllers = append(controllers, map[string]string{"button": "Keypad", "encoder": "Knob", "touch": "Information"}[kind])
					}
					item["Controllers"] = controllers
				}
				if len(official.ActionFields(d.Slug, a)) == 0 {
					delete(item, "PropertyInspectorPath")
				}
				actions = append(actions, item)
			}
			platformKey := map[string]string{"windows": "CodePathWin", "darwin": "CodePathMac", "linux": "CodePathLinux"}[osname]
			manifest := map[string]any{"StreamDockManager": map[string]any{"MinimumVersion": d.MinimumManager, "RequiredCapabilities": d.RequiredCapabilities}, "UUID": official.ID(d.Slug), "Name": d.Name, "Version": d.Version, "Author": "Stream Dock Manager contributors", "Description": d.Name + " for Stream Dock Manager", "SDKVersion": 1, "CodePath": entry, "Icon": "icon.svg", "Category": d.Name, "CategoryIcon": "icon.svg", "Actions": actions, platformKey: entry, "Software": map[string]any{"MinimumVersion": "3.10.205.0918"}, "OS": []map[string]string{{"Platform": map[string]string{"darwin": "mac", "windows": "windows", "linux": "linux"}[osname], "MinimumVersion": map[string]string{"darwin": "14", "windows": "11", "linux": "0"}[osname]}}}
			metadata := manifest["StreamDockManager"].(map[string]any)
			metadata["ManifestDialect"] = "vsd"
			metadata["Capabilities"] = official.Descriptor(d)
			if slots := official.PackageSetupSlots(d.Slug); slots != nil {
				metadata["SetupSlots"] = slots
			}
			if permissions := official.PackagePermissions(d.Slug); permissions != nil {
				metadata["Permissions"] = permissions
			}
			mb, _ := json.MarshalIndent(manifest, "", "    ")
			files := map[string]packageFile{"manifest.json": {data: mb}, entry: {path: path}}
			assets, err := builder.inspectorAssets(d)
			if err != nil {
				return err
			}
			for name, data := range assets {
				files[name] = packageFile{data: data}
			}
			for _, name := range []string{"LICENSE", "README.md", "examples/new-plugins.json"} {
				files[name] = packageFile{path: filepath.Join(builder.root, name)}
			}
			if d.Slug == "sdm-vault-example" {
				files["README.md"] = packageFile{path: filepath.Join(builder.root, "internal", "plugins", d.Slug, "README.md")}
				inputs, err := builder.prepareArchive(io.Discard, map[string]packageFile{"profiles.json": {path: filepath.Join(builder.root, "internal", "plugins", d.Slug, "variables.json")}})
				if err != nil {
					return err
				}
				var macros bytes.Buffer
				if err = builder.archiveTo(&macros, "", inputs, "", ""); err != nil {
					return err
				}
				files["examples/vault-variables.zip"] = packageFile{data: macros.Bytes()}
			}
			files["THIRD_PARTY_LICENSES.txt"] = packageFile{data: notices}
			files["THIRD_PARTY_NOTICES.md"] = packageFile{data: packageNotices(d)}
			packageName := official.ID(d.Slug) + ".zip"
			pb, e := builder.archiveCached(filepath.Join(packageOut, packageName), official.ID(d.Slug)+".sdPlugin/", files, entry)
			if e != nil {
				return e
			}
			packages = append(packages, map[string]any{"files": pb.Files, "os": map[string]string{"darwin": "macos", "windows": "windows", "linux": "linux"}[osname], "id": official.ID(d.Slug), "version": d.Version, "path": filepath.ToSlash(filepath.Join("packages", osname, variant, packageName)), "sha256": pb.SHA256, "size": pb.Size, "architectures": archs, "minimum_manager": d.MinimumManager, "required_capabilities": d.RequiredCapabilities})
		}
		results[indexes[d.Slug]] = packages
		return nil
	}); err != nil {
		return err
	}
	packages := []map[string]any{}
	for _, result := range results {
		packages = append(packages, result...)
	}
	platformName := osname
	if osname == "darwin" {
		platformName = "macos"
	}
	catalog := map[string]any{"schema_version": 1, "product": "plugins", "source_fingerprint": builder.fingerprint(), "packages": packages, "support_policy": official.Policy()}
	if builder.activePlan != nil {
		catalog["repository"] = builder.activePlan.Repository
		catalog["channel"] = builder.activePlan.Channel
		catalog["commit"] = builder.activePlan.Commit
		catalog["release_tag"] = builder.activePlan.Tag
	}
	inventoryDirectory := filepath.Join(builder.root, "dist")
	if builder.targetArch == "universal" {
		inventoryDirectory = filepath.Join(inventoryDirectory, "optional")
		if e = os.MkdirAll(inventoryDirectory, 0755); e != nil {
			return e
		}
		platformName += "-universal"
	}
	if e = writeJSON(filepath.Join(inventoryDirectory, "catalog-"+platformName+".json"), catalog); e != nil {
		return e
	}
	var checksums strings.Builder
	for _, p := range packages {
		fmt.Fprintf(&checksums, "%s  %s\n", p["sha256"], p["path"])
	}
	return os.WriteFile(filepath.Join(inventoryDirectory, "SHA256SUMS-"+platformName), []byte(checksums.String()), 0644)
}

// fingerprint hashes the effective packaging inputs
func (builder *buildRunner) fingerprint() string {
	h := sha256.New()
	dependency, err := builder.sdkConfiguration(builder.environment)
	if err != nil {
		panic(err)
	}
	data, _ := json.Marshal(dependency)
	h.Write(data)
	_ = filepath.WalkDir(builder.root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if p != builder.root && (d.Name() == ".git" || d.Name() == ".cache" || d.Name() == "dist" || p == filepath.Join(builder.root, "artifacts")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".go") || strings.Contains(p, "inspectors") || d.Name() == "go.mod" || d.Name() == "go.sum" || d.Name() == "LICENSE" || strings.HasSuffix(p, ".md") {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(builder.root, p)
			_, _ = io.WriteString(h, filepath.ToSlash(rel))
			_, _ = h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// main runs the pluginbuild command
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root, err := os.Getwd()
	if err == nil {
		var config buildConfig
		config, err = parseConfig(os.Args[1:], root, os.Environ(), os.Stderr)
		if err == nil {
			var builder *buildRunner
			builder, err = newBuildRunner(ctx, config, os.Stdout, os.Stderr)
			if err == nil {
				err = builder.run()
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run dispatches the selected build-tool operation
func (builder *buildRunner) run() error {
	action := builder.action

	switch action {
	case "catalog":
		ds, err := builder.definitions()
		if err != nil {
			return err
		}
		sourceFingerprint := builder.fingerprint()
		entries := make([]any, 0, len(ds))
		for _, d := range ds {
			entries = append(entries, struct {
				official.Definition
				ID                 string           `json:"id"`
				Capabilities       sdk.Capabilities `json:"capabilities"`
				SourceFingerprint  string           `json:"source_fingerprint"`
				Icon               string           `json:"icon"`
				SupportedPlatforms []string         `json:"supported_platforms"`
			}{d, official.ID(d.Slug), official.Descriptor(d), sourceFingerprint, "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(official.PluginAssets(d)["icon-dark.svg"]), official.SupportedPlatforms(d.Slug)})
		}
		encoder := json.NewEncoder(builder.stdout)
		encoder.SetIndent("", "    ")
		return encoder.Encode(entries)
	case "assets":
		ds, err := builder.definitions()
		if err != nil {
			return err
		}
		// A full regeneration owns this output tree. Drop retired modules so
		// local pickers and inspector checks cannot rediscover stale packages
		ui := filepath.Join(builder.root, "dist", "ui")
		if builder.assetsOutput != "" {
			ui = builder.assetsOutput
			if err := os.Mkdir(ui, 0755); err != nil {
				return err
			}
		} else if builder.selected == "" {
			if err := os.RemoveAll(ui); err != nil {
				return err
			}
		}
		for _, d := range ds {
			folder := filepath.Join(ui, d.Slug)
			files, err := builder.inspectorAssets(d)
			if err != nil {
				return err
			}
			// Descriptors are staging-only; packages carry this in their manifest
			descriptor, err := json.MarshalIndent(official.Descriptor(d), "", "    ")
			if err != nil {
				return err
			}
			files["descriptor.json"] = descriptor
			for name, data := range files {
				path := filepath.Join(folder, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					return err
				}
			}
		}
		fmt.Fprintf(builder.stdout, "Staged %d plugin UI modules in %s\n", len(ds), ui)
		return nil
	case "build":
		return builder.compile(runtime.GOOS, runtime.GOARCH)
	case "cross":
		for _, t := range builder.crossTargets() {
			if e := builder.compile(t[0], t[1]); e != nil {
				return e
			}
		}
		return nil
	case "package":
		if builder.targetOS != "windows" && builder.targetOS != "linux" && builder.targetOS != "darwin" {
			return fmt.Errorf("unsupported OS")
		}
		variants, err := builder.packageVariants(builder.targetOS, builder.targetArch)
		if err != nil {
			return err
		}
		if builder.targetOS == "darwin" {
			if builder.targetArch == "universal" {
				variants = []string{"amd64", "arm64"}
			}
			for _, a := range variants {
				if e := builder.compile(builder.targetOS, a); e != nil {
					return e
				}
			}
		} else {
			if e := builder.compile(builder.targetOS, "amd64"); e != nil {
				return e
			}
		}
		return builder.pack(builder.targetOS)
	case "check":
		if e := builder.command("go", "vet", "./..."); e != nil {
			return e
		}
		return builder.command("go", "build", "./...")
	}
	return fmt.Errorf("unknown command %s", action)
}

// crossTargets selects the supported cross-compilation targets
func (builder *buildRunner) crossTargets() [][2]string {
	targets := [][2]string{{"windows", "amd64"}, {"linux", "amd64"}}
	arches, _ := builder.packageVariants("darwin", "")
	for _, arch := range arches {
		targets = append(targets, [2]string{"darwin", arch})
	}
	return targets
}
