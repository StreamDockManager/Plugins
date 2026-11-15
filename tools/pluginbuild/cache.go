package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/packaging"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type cacheRecord struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

type binaryBuildConfiguration struct {
	packaging.BinaryBuildConfiguration
	WindowsMemory string
}
type sdkBuildInput = packaging.SDKBuildInput

// binaryConfiguration collects the compiler and target settings used by the binary cache
func (builder *buildRunner) binaryConfiguration(environment []string) (binaryBuildConfiguration, error) {
	configuration, err := packaging.BinaryConfiguration(builder.ctx, builder.root, environment)
	return binaryBuildConfiguration{BinaryBuildConfiguration: configuration}, err
}

// sdkConfiguration resolves the SDK configuration used for the build
func (builder *buildRunner) sdkConfiguration(environment []string) (sdkBuildInput, error) {
	return packaging.SDKConfiguration(builder.ctx, builder.root, environment)
}

const sdkModulePath = "github.com/StreamDockManager/Plugin-SDK/Go"

// cached validates and reuses an existing binary-cache entry
func (builder *buildRunner) cached(path, key string) bool {
	return !builder.force && packaging.Cached(path, key)
}

// remember records the built binary and its cache identity
func remember(path, key string) error { return packaging.Remember(path, key) }

// binaryKey derives the cache key from the effective build inputs
func (builder *buildRunner) binaryKey(d official.Definition, osname, arch string, configuration binaryBuildConfiguration) (string, error) {
	h := sha256.New()
	frame, err := sdk.FrameCapabilities(official.Descriptor(d))
	if err != nil {
		return "", err
	}
	arguments, err := json.Marshal(binaryArguments(osname, configuration.SDK.ImportPath, frame))
	if err != nil {
		return "", err
	}
	if err := rejectDependencyOverlay(configuration); err != nil {
		return "", err
	}
	dependencies, err := builder.dependencies(d.Slug, osname, arch, true)
	if err != nil {
		return "", err
	}
	// Selected SDK files are hashed in the actual import closure, not its entire
	// module (which includes build tools and unrelated examples)
	configuration.SDK.Content = "selected-dependency-closure-v1"
	settings, err := json.Marshal(configuration)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(h, "binary-v2-selected-dependencies\x00%s\x00%s\x00%s\x00%s\x00%s\x00", osname, arch, settings, arguments, dependencies.fingerprint)
	// Preserve root resolution inputs, including optional go.sum and workspace
	names := []string{filepath.Join(builder.root, "go.mod"), filepath.Join(builder.root, "go.sum")}
	var env map[string]string
	if err := json.Unmarshal(configuration.Go, &env); err != nil {
		return "", err
	}
	if work := env["GOWORK"]; work != "" && work != "off" {
		names = append(names, work, work+".sum")
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if os.IsNotExist(err) {
			fmt.Fprintf(h, "%s:absent\x00", name)
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s:%d\x00", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parallelDefinitions processes plugin definitions under the shared concurrency limit
func (builder *buildRunner) parallelDefinitions(ds []official.Definition, fn func(official.Definition) error) error {
	return packaging.Parallel(builder.ctx, ds, builder.jobs, func(d official.Definition) error {
		if err := fn(d); err != nil {
			return fmt.Errorf("%s: %w", d.Slug, err)
		}
		return nil
	})
}

// archiveCached verifies a package independently of binary eligibility. Every
// generated asset, inspector, notice, manifest and executable participates
type packageArtifact struct {
	SHA256 string
	Size   int64
	Files  map[string]string
}

// artifact streams the built file to measure its SHA-256 and byte size
func (builder *buildRunner) artifact(path string) (packageArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return packageArtifact{}, err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, cancellableReader{file, builder.ctx})
	return packageArtifact{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, err
}

// archiveCached validates and reuses a final archive-cache entry
func (builder *buildRunner) archiveCached(path, prefix string, files map[string]packageFile, executable string) (packageArtifact, error) {
	h := sha256.New()
	fmt.Fprintf(h, "package-v1\x00%s\x00%s\x00%s\x00%s\x00", prefix, executable, compressionPolicy, runtime.Version())
	inputs, err := builder.prepareArchive(h, files)
	if err != nil {
		return packageArtifact{}, err
	}
	key := hex.EncodeToString(h.Sum(nil))
	fileHashes := map[string]string{}
	for name, input := range inputs {
		fileHashes[name] = input.digest
	}
	if builder.cached(path, key) {
		result, err := builder.artifact(path)
		result.Files = fileHashes
		return result, err
	}
	started := time.Now()
	output, err := os.CreateTemp(filepath.Dir(path), ".package-*")
	if err != nil {
		return packageArtifact{}, err
	}
	defer func() { output.Close(); os.Remove(output.Name()) }()
	if err = output.Chmod(0644); err != nil {
		return packageArtifact{}, err
	}
	if err = builder.archiveTo(output, prefix, inputs, executable, filepath.Dir(path)); err != nil {
		return packageArtifact{}, err
	}
	if err = output.Sync(); err != nil {
		return packageArtifact{}, err
	}
	if err = output.Close(); err != nil {
		return packageArtifact{}, err
	}
	result, err := builder.artifact(output.Name())
	if err != nil {
		return packageArtifact{}, err
	}
	// Publish the fully written temporary archive only after hashing and closing it
	if err = os.Rename(output.Name(), path); err != nil {
		return packageArtifact{}, err
	}
	fmt.Fprintf(builder.stderr, "Packaged %s: %d bytes in %s\n", path, result.Size, time.Since(started))
	if err = remember(path, key); err != nil {
		return packageArtifact{}, err
	}
	result.Files = fileHashes
	return result, nil
}
