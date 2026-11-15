package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// memoryFiles constructs an in-memory archive input inventory
func memoryFiles(files map[string][]byte) map[string]packageFile {
	inputs := make(map[string]packageFile, len(files))
	for name, data := range files {
		inputs[name] = packageFile{data: data}
	}
	return inputs
}

// Existing format fixtures use this in-memory test adapter, never production
func archive(prefix string, files map[string][]byte, executable string) ([]byte, error) {
	builder := &buildRunner{ctx: context.Background()}
	inputs, err := builder.prepareArchive(io.Discard, memoryFiles(files))
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "plugin-archive-test-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	var output bytes.Buffer
	err = builder.archiveTo(&output, prefix, inputs, executable, directory)
	return output.Bytes(), err
}

// TestFileBackedArchiveMatchesOriginalBytesAndCacheKeys verifies file backed archive matches original bytes and cache keys
func TestFileBackedArchiveMatchesOriginalBytesAndCacheKeys(t *testing.T) {
	builder := testRunner(t)
	random := rand.New(rand.NewPCG(12, 34))
	for _, size := range []int{0, 17, 64 << 10, (1 << 20) + 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(random.Uint32())
			}
			files := map[string][]byte{"plugin": payload, "manifest.json": []byte(`{"Version":"0.1.0"}`), "empty": {}, "icon.svg": []byte("<svg/>末")}
			if size >= 64<<10 {
				files["large-asset.bin"] = payload
			}
			want, err := referenceArchive("fixture.sdPlugin/", files, "plugin")
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			inputs := map[string]packageFile{}
			for name, data := range files {
				path := filepath.Join(directory, name)
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				inputs[name] = packageFile{path: path}
			}
			path := filepath.Join(directory, "package.zip")
			info, err := builder.archiveCached(path, "fixture.sdPlugin/", inputs, "plugin")
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) || info.Size != int64(len(want)) || info.SHA256 != hash(want) {
				t.Fatal("file-backed archive changed deterministic bytes", err)
			}
			decoded := archiveFiles(t, got)
			h := sha256.New()
			fmt.Fprintf(h, "package-v1\x00%s\x00%s\x00%s\x00%s\x00", "fixture.sdPlugin/", "plugin", compressionPolicy, runtime.Version())
			for _, name := range inputNames(files) {
				data := files[name]
				fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
				h.Write(data)
				if !bytes.Equal(decoded["fixture.sdPlugin/"+name], data) || info.Files[name] != hash(data) {
					t.Fatal("payload or catalog digest changed", name)
				}
			}
			recordBytes, _ := os.ReadFile(path + ".build.json")
			var record cacheRecord
			if json.Unmarshal(recordBytes, &record) != nil || record.Input != hex.EncodeToString(h.Sum(nil)) || record.Output != info.SHA256 {
				t.Fatal("cache framing changed", string(recordBytes))
			}
			before, _ := os.Stat(path)
			again, err := builder.archiveCached(path, "fixture.sdPlugin/", inputs, "plugin")
			after, _ := os.Stat(path)
			if err != nil || again.SHA256 != info.SHA256 || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("verified cache hit rewrote package", err)
			}
			for _, pattern := range []string{".lzma-*", ".package-*"} {
				if temporary, _ := filepath.Glob(filepath.Join(directory, pattern)); len(temporary) != 0 {
					t.Fatal("temporary spool retained", temporary)
				}
			}
		})
	}
}

// TestArchiveChangedSourceCancellationAndFailureCleanup verifies archive changed source cancellation and failure cleanup
func TestArchiveChangedSourceCancellationAndFailureCleanup(t *testing.T) {
	builder := testRunner(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "input")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	files := map[string]packageFile{"plugin": {path: path}}
	inputs, err := builder.prepareArchive(io.Discard, files)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = builder.archiveTo(io.Discard, "fixture/", inputs, "plugin", directory); err == nil {
		t.Fatal("same-size source mutation accepted")
	}
	if temporary, _ := filepath.Glob(filepath.Join(directory, ".lzma-*")); len(temporary) != 0 {
		t.Fatal("failed compression leaked spool", temporary)
	}
	inputs, err = builder.prepareArchive(io.Discard, files)
	if err != nil {
		t.Fatal(err)
	}
	if err = builder.archiveTo(failedArchiveWriter{}, "fixture/", inputs, "plugin", directory); err == nil {
		t.Fatal("failed output write succeeded")
	}
	if temporary, _ := filepath.Glob(filepath.Join(directory, ".lzma-*")); len(temporary) != 0 {
		t.Fatal("output failure leaked spool", temporary)
	}
	output := filepath.Join(directory, "prior.zip")
	if err = os.WriteFile(output, []byte("existing artifact"), 0644); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	builder.ctx = cancelled
	if _, err = builder.archiveCached(output, "fixture/", files, "plugin"); err == nil {
		t.Fatal("cancelled packaging succeeded")
	}
	if got, _ := os.ReadFile(output); string(got) != "existing artifact" {
		t.Fatal("failed packaging replaced existing archive")
	}
}

type failedArchiveWriter struct{}

// Write writes the captured output through the configured writer
func (failedArchiveWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestLegacyCompressionCacheIsNotReused verifies legacy compression cache is not reused
func TestLegacyCompressionCacheIsNotReused(t *testing.T) {
	builder := testRunner(t)
	path := filepath.Join(t.TempDir(), "plugin.zip")
	files := memoryFiles(map[string][]byte{"plugin": []byte("payload"), "manifest.json": []byte("{}")})
	h := sha256.New()
	fmt.Fprintf(h, "package-v1\x00%s\x00%s\x00%s\x00%s\x00", "fixture/", "plugin", "zip14:lzma1:itchio-d3e24e3e3d49:sdk4.65:level5:dict=8388608:bt4:fb128:lc3:lp0:pb2:eos;support=deflate-default", runtime.Version())
	if _, err := builder.prepareArchive(h, files); err != nil {
		t.Fatal(err)
	}
	oldKey := hex.EncodeToString(h.Sum(nil))
	if err := os.WriteFile(path, []byte("old archive"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := remember(path, oldKey); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.archiveCached(path, "fixture/", files, "plugin"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archiveFiles(t, data)["fixture/plugin"], []byte("payload")) {
		t.Fatal("legacy cache reused")
	}
	recordBytes, _ := os.ReadFile(path + ".build.json")
	var record cacheRecord
	if json.Unmarshal(recordBytes, &record) != nil || record.Input == oldKey {
		t.Fatal("legacy compression cache key retained")
	}
}
