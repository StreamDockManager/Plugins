package main

import (
	"bytes"
	"encoding/base64"
	stdbinary "encoding/binary"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

// Go 1.27.4 links a 32 MiB FIPS entropy scratch array into every crypto user,
// including non-FIPS plugins. Windows charges it to each executable's commit;
// it can also become resident. Keep the full buffer and entropy implementation,
// but allocate it once on the first FIPS entropy request. No installed source is
// modified. Remove this workaround once the selected upstream toolchain fixes
// https://github.com/golang/go/issues/81505.
const entropySourceHash = "bd3c834a29e31c93e56d81da54d4a874561814a86550de1456c8d6d40a9c5fda"
const entropyRelativePath = "src/crypto/internal/fips140/drbg/entropy_fips140.go"

type pluginBuild struct {
	command     string
	arguments   []string
	environment []string
}

// lazyEntropySource builds the Windows entropy shim without eager initialization
func lazyEntropySource(source []byte) ([]byte, error) {
	source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	if hash(source) != entropySourceHash {
		return nil, fmt.Errorf("Go FIPS entropy source differs from reviewed Go 1.27.4; review the Windows memory workaround before building")
	}
	text := string(source)
	text = strings.Replace(text, `import entropy "crypto/internal/entropy/v1.0.0"`, "import (\n entropy \"crypto/internal/entropy/v1.0.0\"\n \"sync\"\n)", 1)
	text = strings.Replace(text, "var memory entropy.ScratchBuffer", "// Allocate the unchanged scratch buffer only when FIPS entropy is requested.\nvar memory = sync.OnceValue(func() *entropy.ScratchBuffer { return new(entropy.ScratchBuffer) })", 1)
	text = strings.ReplaceAll(text, "entropy.Seed(&memory)", "entropy.Seed(memory())")
	return format.Source([]byte(text))
}

// prepareWindowsMemoryBuild prepares the scoped Windows memory-build inputs
func (builder *buildRunner) prepareWindowsMemoryBuild(osname string, environment []string, configuration *binaryBuildConfiguration) (pluginBuild, error) {
	build := pluginBuild{command: "go", environment: environment}
	if osname != "windows" {
		return build, nil
	}
	var toolchain struct{ GOVERSION, GOROOT, GOFLAGS string }
	if err := json.Unmarshal(configuration.Go, &toolchain); err != nil {
		return build, err
	}
	query := builder.newCommand("go", "env", "GOFIPS140")
	query.Dir, query.Env = builder.root, environment
	fips, err := query.Output()
	if err != nil {
		return build, fmt.Errorf("read FIPS build setting: %w", err)
	}
	configuration.WindowsMemory = "stock:" + strings.TrimSpace(string(fips))
	// Explicit FIPS module selections must use the unmodified selected module
	if strings.TrimSpace(string(fips)) != "off" {
		return build, nil
	}
	if toolchain.GOVERSION != "go1.27.4" {
		return build, fmt.Errorf("Windows memory workaround requires reviewed Go 1.27.4, got %s", toolchain.GOVERSION)
	}
	if strings.Contains(toolchain.GOFLAGS, "-overlay") {
		return build, fmt.Errorf("custom GOFLAGS -overlay conflicts with the Windows memory build overlay")
	}
	source, err := os.ReadFile(filepath.Join(toolchain.GOROOT, filepath.FromSlash(entropyRelativePath)))
	if err != nil {
		return build, err
	}
	patched, err := lazyEntropySource(source)
	if err != nil {
		return build, err
	}
	configuration.WindowsMemory = "lazy-entropy-v1:" + hash(patched)
	directory := filepath.Join(builder.root, "dist", ".build", "windows-memory", hash([]byte(toolchain.GOROOT))[:16])
	if err = os.MkdirAll(directory, 0755); err != nil {
		return build, err
	}
	// A downloaded toolchain lives below GOMODCACHE, where go -overlay forbids
	// replacements. An immutable directory alias gives the standard library its
	// own GOROOT path; the overlay still changes only build input, never files
	alias := filepath.Join(directory, "goroot")
	if err = builder.toolchainAlias(alias, toolchain.GOROOT); err != nil {
		return build, err
	}
	replacement := filepath.Join(directory, "entropy_fips140.go")
	if err = writeBuildInput(replacement, patched); err != nil {
		return build, err
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{
		map[string]string{filepath.Join(alias, filepath.FromSlash(entropyRelativePath)): replacement},
	})
	if err != nil {
		return build, err
	}
	overlayPath := filepath.Join(directory, "overlay.json")
	if err = writeBuildInput(overlayPath, overlay); err != nil {
		return build, err
	}
	build.arguments = []string{"-overlay=" + overlayPath}
	build.environment = append(append([]string{}, environment...), "GOROOT="+alias, "GOTOOLCHAIN=local")
	build.command = filepath.Join(alias, "bin", "go")
	if runtime.GOOS == "windows" {
		build.command += ".exe"
	}
	return build, nil
}

// writeBuildInput writes an owned build input for the compiler
func writeBuildInput(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return os.WriteFile(path, data, 0644)
}

// toolchainAlias selects the compiler alias for the configured toolchain
func (builder *buildRunner) toolchainAlias(alias, target string) error {
	check := func() bool {
		left, err := os.Stat(alias)
		if err != nil {
			return false
		}
		right, err := os.Stat(target)
		return err == nil && os.SameFile(left, right)
	}
	if check() {
		return nil
	}
	if _, err := os.Lstat(alias); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("toolchain alias %s does not refer to selected GOROOT", alias)
	}
	var err error
	if runtime.GOOS == "windows" {
		// Junction creation needs no Developer Mode or administrator symlink
		// privilege. Encode the script so paths remain literal PowerShell data
		quote := func(path string) string { return "'" + strings.ReplaceAll(path, "'", "''") + "'" }
		script := "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path " + quote(alias) + " -Target " + quote(target) + " | Out-Null"
		words := utf16.Encode([]rune(script))
		encoded := make([]byte, len(words)*2)
		for i, word := range words {
			stdbinary.LittleEndian.PutUint16(encoded[i*2:], word)
		}
		command := builder.newCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
		err = command.Run()
	} else {
		err = os.Symlink(target, alias)
	}
	// Another build may have created the identical alias concurrently
	if check() {
		return nil
	}
	return fmt.Errorf("create immutable Go toolchain alias: %w", err)
}
