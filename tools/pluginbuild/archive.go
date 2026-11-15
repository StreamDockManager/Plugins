package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
)

// Generated assets stay in memory; binaries and other existing files are opened
// only while hashing or writing their entry. No package-sized buffer is retained
type packageFile struct {
	data []byte
	path string
}

// open reads an in-memory payload or opens its file-backed source
func (f packageFile) open() (io.ReadCloser, int64, error) {
	if f.path == "" {
		return io.NopCloser(bytes.NewReader(f.data)), int64(len(f.data)), nil
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

type archiveInput struct {
	packageFile
	size   int64
	digest string
}

type cancellableReader struct {
	io.Reader
	ctx context.Context
}

// Read stops archive input streaming when the build context is cancelled
func (r cancellableReader) Read(p []byte) (int, error) {
	if r.ctx != nil && r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	return r.Reader.Read(p)
}

// inputNames returns the archive input names in deterministic order
func inputNames[T any](files map[string]T) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The framing and byte order remain identical to the original in-memory key
func (builder *buildRunner) prepareArchive(h io.Writer, files map[string]packageFile) (map[string]archiveInput, error) {
	inputs := make(map[string]archiveInput, len(files))
	for _, name := range inputNames(files) {
		file := files[name]
		reader, size, err := file.open()
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, size)
		digest := sha256.New()
		n, err := io.Copy(io.MultiWriter(h, digest), cancellableReader{reader, builder.ctx})
		reader.Close()
		if err != nil {
			return nil, err
		}
		// Reject an input that changed size while its cache identity was being hashed
		if n != size {
			return nil, fmt.Errorf("package input changed while hashing: %s", name)
		}
		inputs[name] = archiveInput{packageFile: file, size: size, digest: hex.EncodeToString(digest.Sum(nil))}
	}
	return inputs, nil
}

// copy copies one verified input into the archive writer
func (f archiveInput) copy(ctx context.Context, w io.Writer) (int64, error) {
	reader, _, err := f.open()
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), cancellableReader{reader, ctx})
	if err == nil && (n != f.size || hex.EncodeToString(h.Sum(nil)) != f.digest) {
		err = fmt.Errorf("package input changed after hashing: %s", f.path)
	}
	return n, err
}

// archiveTo writes the prepared plugin archive to its destination
func (builder *buildRunner) archiveTo(out io.Writer, prefix string, files map[string]archiveInput, executable, _ string) (err error) {
	z := zip.NewWriter(out)
	// Keep all owned ZIP entries on the shared DEFLATE level-six policy
	z.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 6) })
	defer func() {
		if err != nil {
			_ = z.Close()
		}
	}()
	for _, name := range inputNames(files) {
		input := files[name]
		h := &zip.FileHeader{Name: prefix + name, Method: zip.Deflate}
		h.SetMode(0644)
		if name == executable {
			h.SetMode(0755)
		}
		var writer io.Writer
		writer, err = z.CreateHeader(h)
		if err == nil {
			_, err = input.copy(builder.ctx, writer)
		}

		if err != nil {
			return err
		}
	}
	return z.Close()
}
