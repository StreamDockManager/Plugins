package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/StreamDockManager/Plugin-SDK/Go/packaging"
)

// Invocation-local only: never trust an earlier process's dependency inventory
// Keys are also scoped to the complete effective environment and working tree
type dependencyAnalysisCache struct {
	mu       sync.Mutex
	analyses map[string]*dependencyAnalysis
	notices  map[string][]byte
}
type dependencyAnalysis struct {
	packages    []dependencyPackage
	modules     []noticeModule
	files       []string
	resolution  []string
	fingerprint string
	watch       string
}
type dependencyPackage struct {
	Standard                                                                                                            bool
	Dir, ImportPath                                                                                                     string
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles []string
	Module                                                                                                              *packaging.Module
	Error                                                                                                               *struct{ Err string }
	DepsErrors                                                                                                          []struct{ Err string }
}

// hashDependencyFiles hashes dependency files into the build fingerprint
func hashDependencyFiles(files []string) (string, error) {
	h := sha256.New()
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dependencies collects the build dependency closure
func (builder *buildRunner) dependencies(slug, osname, arch string, refresh bool) (*dependencyAnalysis, error) {
	environment := builder.binaryEnvironment(osname, arch)
	keyBytes, _ := json.Marshal([]any{builder.root, slug, environment})
	key := string(keyBytes)
	cache := &builder.dependencyCache
	// Serialize analysis and publication; go list itself is bounded to one process
	// and cannot multiply the compiler worker budget
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !refresh && cache.analyses[key] != nil {
		previous := cache.analyses[key]
		// Source/module content changed since compilation: recompute rather than
		// return a stale license dependency set
		current, err := dependencyWatch(previous)
		if err == nil && current == previous.watch {
			return previous, nil
		}
	}
	c := builder.newCommand("go", "list", "-deps", "-json", "./cmd/"+slug)
	c.Dir, c.Env = builder.root, environment
	var stderr bytes.Buffer
	c.Stderr = &stderr
	output, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("analyze %s %s/%s dependencies: %w: %s", slug, osname, arch, err, stderr.String())
	}
	result := &dependencyAnalysis{resolution: []string{filepath.Join(builder.root, "go.sum")}}
	if work := environmentValue(environment, "GOWORK"); work != "" && work != "auto" && work != "off" {
		result.resolution = append(result.resolution, work, work+".sum")
	} else if work != "off" {
		for dir := builder.root; ; dir = filepath.Dir(dir) {
			result.resolution = append(result.resolution, filepath.Join(dir, "go.work"), filepath.Join(dir, "go.work.sum"))
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	files := map[string]bool{}
	modules := map[string]noticeModule{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg dependencyPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if pkg.Error != nil || len(pkg.DepsErrors) > 0 {
			return nil, fmt.Errorf("incomplete dependency analysis for %s", pkg.ImportPath)
		}
		if pkg.Dir == "" || pkg.ImportPath == "" {
			return nil, fmt.Errorf("dependency has no source directory or identity")
		}
		result.packages = append(result.packages, pkg)
		// Standard library content follows the selected compiler identity
		// WindowsMemory separately records our reviewed toolchain patch
		if pkg.Standard {
			continue
		}
		for _, group := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles} {
			for _, name := range group {
				if !filepath.IsAbs(name) {
					name = filepath.Join(pkg.Dir, name)
				}
				files[name] = true
			}
		}
		if pkg.Module != nil {
			module := *pkg.Module
			if module.Replace != nil {
				module.Dir = module.Replace.Dir
				module.Version = module.Replace.Version
			}
			if module.Dir == "" {
				return nil, fmt.Errorf("dependency module directory unavailable: %s", module.Path)
			}
			files[filepath.Join(module.Dir, "go.mod")] = true
			result.resolution = append(result.resolution, filepath.Join(module.Dir, "go.sum"))
			mainModule := false
			if module.Main {
				moduleInfo, moduleErr := os.Stat(module.Dir)
				rootInfo, rootErr := os.Stat(builder.root)
				mainModule = moduleErr == nil && rootErr == nil && os.SameFile(moduleInfo, rootInfo)
			}
			if !mainModule {
				modules[module.Path+"@"+module.Version+"@"+module.Dir] = module
			}
		}
	}
	if len(result.packages) == 0 {
		return nil, fmt.Errorf("empty dependency analysis")
	}
	for name := range files {
		result.files = append(result.files, name)
	}
	sort.Strings(result.files)
	for _, module := range modules {
		result.modules = append(result.modules, module)
	}
	sort.Slice(result.modules, func(i, j int) bool { return result.modules[i].Path < result.modules[j].Path })
	result.fingerprint, err = hashDependencyFiles(result.files)
	if err != nil {
		return nil, err
	}
	result.watch, err = dependencyWatch(result)
	if err != nil {
		return nil, err
	}
	if cache.analyses == nil {
		cache.analyses = map[string]*dependencyAnalysis{}
	}
	cache.analyses[key] = result
	return result, nil
}

// Watch directory membership as well as selected contents: adding a Go source,
// import or embedded file must refresh go list before reusing license analysis
func dependencyWatch(analysis *dependencyAnalysis) (string, error) {
	h := sha256.New()
	content, err := hashDependencyFiles(analysis.files)
	if err != nil {
		return "", err
	}
	h.Write([]byte(content))
	for _, name := range analysis.resolution {
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
	dirs := map[string]bool{}
	for _, pkg := range analysis.packages {
		if pkg.Standard {
			continue
		}
		dirs[pkg.Dir] = true
		if len(pkg.EmbedFiles) > 0 {
			// Watch nested directory inventories so new embedded assets invalidate the analysis
			err := filepath.WalkDir(pkg.Dir, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					dirs[path] = true
				}
				return nil
			})
			if err != nil {
				return "", err
			}
		}
	}
	names := make([]string, 0, len(dirs))
	for dir := range dirs {
		names = append(names, dir)
	}
	sort.Strings(names)
	for _, dir := range names {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			fmt.Fprintf(h, "%s/%s:%s\x00", dir, entry.Name(), entry.Type())
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Cache rendering by the complete license inputs, including absent optional
// files. Local SDK license edits are therefore observed even within one run
func (builder *buildRunner) moduleNotices(modules []noticeModule) ([]byte, error) {
	identity, err := json.Marshal(modules)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(identity)
	for _, module := range modules {
		for _, name := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "NOTICE", "NOTICE.txt", "NOTICE.md"} {
			path := filepath.Join(module.Dir, name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				fmt.Fprintf(h, "%s:absent\x00", path)
				continue
			}
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(h, "%s:%d\x00", path, len(data))
			h.Write(data)
		}
	}
	key := hex.EncodeToString(h.Sum(nil))
	cache := &builder.dependencyCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if data, ok := cache.notices[key]; ok {
		return append([]byte(nil), data...), nil
	}
	data, err := packaging.ReadNotices(modules)
	if err != nil {
		return nil, err
	}
	if cache.notices == nil {
		cache.notices = map[string][]byte{}
	}
	cache.notices[key] = append([]byte(nil), data...)
	return data, nil
}

// go list interprets overlays but reports original filenames. Refuse custom
// binary-cache eligibility until overlay contents can be resolved equivalently
func rejectDependencyOverlay(configuration binaryBuildConfiguration) error {
	var env map[string]string
	if err := json.Unmarshal(configuration.Go, &env); err != nil {
		return err
	}
	if strings.Contains(env["GOFLAGS"], "-overlay") {
		return fmt.Errorf("dependency-aware cache does not support GOFLAGS -overlay; remove overlay for packaged release builds")
	}
	return nil
}
