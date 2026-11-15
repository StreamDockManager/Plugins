package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// Run explicitly against final generated packages; routine unit tests stay small
func TestCompressionAcceptance(t *testing.T) {
	destination := os.Getenv("COMPRESSION_REPORT")
	if destination == "" {
		t.Skip("set COMPRESSION_REPORT to measure all final platform packages")
	}
	paths := []string{}
	for _, platform := range []string{"linux", "windows", "macos"} {
		data, err := os.ReadFile("../../dist/catalog-" + platform + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var inventory struct{ Packages []struct{ Path string } }
		if err = json.Unmarshal(data, &inventory); err != nil {
			t.Fatal(err)
		}
		for _, p := range inventory.Packages {
			paths = append(paths, filepath.Join("../../dist", filepath.FromSlash(p.Path)))
		}
	}
	count := len(official.Definitions)
	if len(paths) != count*3 && len(paths) != count*4 {
		t.Fatal("need all default packages, with optional Intel macOS variants", len(paths))
	}

	records := []map[string]any{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		files := archiveFiles(t, data)
		extraction := time.Since(start).Seconds()
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		entryName := ""
		prefix := ""
		for _, f := range reader.File {
			if strings.HasSuffix(f.Name, "/manifest.json") {
				var manifest struct{ CodePath string }
				if err := json.Unmarshal(files[f.Name], &manifest); err != nil {
					t.Fatal(err)
				}
				entryName = manifest.CodePath
				prefix = strings.TrimSuffix(f.Name, "manifest.json")
			}
		}
		if prefix == "" || entryName == "" || len(files[prefix+entryName]) == 0 {
			t.Fatal("executable missing")
		}
		for _, f := range reader.File {
			if f.Method != zip.Deflate {
				t.Fatal("all entries must use ZIP DEFLATE", f.Name)
			}
		}
		relative := map[string][]byte{}
		for name, contents := range files {
			relative[strings.TrimPrefix(name, prefix)] = contents
		}
		start = time.Now()
		repeated, err := archive(prefix, relative, entryName)
		packaging := time.Since(start).Seconds()
		if err != nil || !bytes.Equal(data, repeated) {
			t.Fatal("non-deterministic package", path, err)
		}
		records = append(records, map[string]any{"path": filepath.ToSlash(path), "deflate6_bytes": len(data), "packaging_seconds": packaging, "go_extraction_seconds": extraction})
	}
	out, _ := json.MarshalIndent(records, "", "  ")
	if err := os.WriteFile(destination, append(out, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
