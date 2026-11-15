package main

import (
	"context"
	"debug/pe"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Exercise the actual overlaid standard library, including the lazy FIPS path,
// rather than testing the source replacement text alone
func TestWindowsMemoryBuildPreservesRandomnessAndCapturedOutput(t *testing.T) {
	builder := testRunner(t)
	if runtime.GOOS != "windows" {
		t.Skip("native Windows memory/PE regression")
	}
	builder.root, builder.ctx = t.TempDir(), context.Background()
	builder.setenv("GOWORK", "off")
	builder.setenv("GOFLAGS", "")
	builder.setenv("GOFIPS140", "off")
	if err := os.WriteFile(filepath.Join(builder.root, "go.mod"), []byte("module memory.test\ngo 1.27.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := `package main
import ("crypto/rand"; "crypto/fips140"; "encoding/json"; "fmt"; "os"; "runtime"; "sync")
func main() {
    values := make(chan [32]byte, 32)
    var jobs sync.WaitGroup
    for range 32 { jobs.Go(func() { var b [32]byte; if _, err := rand.Read(b[:]); err != nil { panic(err) }; values <- b }) }
    jobs.Wait(); close(values)
    seen := map[[32]byte]bool{}
    for b := range values { if b == [32]byte{} || seen[b] { panic("invalid randomness") }; seen[b] = true }
    var stats runtime.MemStats; runtime.ReadMemStats(&stats)
    json.NewEncoder(os.Stdout).Encode(map[string]any{"fips": fips140.Enabled(), "heap": stats.HeapAlloc, "values": len(seen)})
    fmt.Fprint(os.Stderr, "captured-stderr")
}`
	if err := os.WriteFile(filepath.Join(builder.root, "main.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	// Read only the effective compiler inputs; this fixture has no SDK dependency
	c := exec.Command("go", "env", "-json", "GOVERSION", "GOROOT", "GOFLAGS")
	c.Dir = builder.root
	c.Env = builder.environment
	settings, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	var configuration binaryBuildConfiguration
	configuration.Go = settings
	build, err := builder.prepareWindowsMemoryBuild("windows", builder.environment, &configuration)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(builder.root, "probe.exe")
	arguments := append(binaryArguments("windows", "unused", "fixture"), build.arguments...)
	c = exec.Command(build.command, append(arguments, "-o", path, ".")...)
	c.Dir, c.Env = builder.root, build.environment
	if output, err := c.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	image, err := pe.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if image.OptionalHeader.(*pe.OptionalHeader64).Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("background plugin still uses the console subsystem")
	}
	if data := image.Section(".data"); data == nil || data.VirtualSize >= 2<<20 {
		t.Fatal("large static entropy buffer remains in executable")
	}
	for _, fips := range []bool{false, true} {
		c = exec.Command(path)
		c.Env = append(builder.environment, "GODEBUG=fips140="+map[bool]string{false: "off", true: "on"}[fips])
		var stderr strings.Builder
		c.Stderr = &stderr
		output, err := c.Output()
		if err != nil || stderr.String() != "captured-stderr" {
			t.Fatalf("run FIPS=%v: %v stderr=%s", fips, err, stderr.String())
		}
		var result struct {
			FIPS   bool
			Heap   uint64
			Values int
		}
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal(err)
		}
		if result.FIPS != fips || result.Values != 32 || (result.Heap >= 32<<20) != fips {
			t.Fatalf("incorrect lazy allocation or random output: %+v", result)
		}
	}
	// An explicit module selection bypasses our standard-library workaround
	builder.setenv("GOFIPS140", "latest")
	stock, err := builder.prepareWindowsMemoryBuild("windows", builder.environment, &configuration)
	if err != nil || len(stock.arguments) != 0 || configuration.WindowsMemory != "stock:latest" {
		t.Fatalf("explicit FIPS selection was modified: %+v %v", stock, err)
	}
}

// TestWindowsEntropyOverlayRejectsUnreviewedSource verifies windows entropy overlay rejects unreviewed source
func TestWindowsEntropyOverlayRejectsUnreviewedSource(t *testing.T) {
	if _, err := lazyEntropySource([]byte("package drbg\nvar memory [1<<25]byte\n")); err == nil {
		t.Fatal("unreviewed standard-library source was accepted")
	}
	// The compiler flags remain target-specific during cross builds
	for _, platform := range []string{"windows", "linux", "darwin"} {
		arguments := strings.Join(binaryArguments(platform, "sdk", "descriptor"), " ")
		if strings.Contains(arguments, "-H=windowsgui") != (platform == "windows") {
			t.Fatal(platform, strconv.Quote(arguments))
		}
	}
}
