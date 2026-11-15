package main

// Independent in-memory level-6 writer checks streaming byte identity
import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"io"
	"sort"
)

// referenceArchive builds the comparison archive with the reference implementation
func referenceArchive(prefix string, files map[string][]byte, executable string) ([]byte, error) {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	z.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 6) })
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h := &zip.FileHeader{Name: prefix + name, Method: zip.Deflate}
		h.SetMode(0644)
		if name == executable {
			h.SetMode(0755)
		}
		data := files[name]
		var w io.Writer
		var e error
		w, e = z.CreateHeader(h)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(data); e != nil {
			return nil, e
		}
	}
	e := z.Close()
	return out.Bytes(), e
}
