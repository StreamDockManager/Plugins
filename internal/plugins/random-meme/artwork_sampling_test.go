package randommeme

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Verify artwork samples match previous pipeline
func TestArtworkSamplesMatchPreviousPipeline(t *testing.T) {
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	testdata := filepath.Join(strings.TrimSpace(string(root)), "src/image/testdata")
	for _, name := range []string{"video-001.jpeg", "video-001.progressive.jpeg", "video-001.png", "video-001.gif", "video-001.interlaced.gif", "video-001.cmyk.jpeg"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(testdata, name))
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			source, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			w, h := cfg.Width, cfg.Height
			if max(w, h) > 320 {
				w, h = max(1, w*320/max(cfg.Width, cfg.Height)), max(1, h*320/max(cfg.Width, cfg.Height))
			}
			expected := image.NewNRGBA(image.Rect(0, 0, w, h))
			bounds := source.Bounds()
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					expected.Set(x, y, source.At(bounds.Min.X+x*cfg.Width/w, bounds.Min.Y+y*cfg.Height/h))
				}
			}
			var output bytes.Buffer
			if err := png.Encode(&output, expected); err != nil {
				t.Fatal(err)
			}
			result, _, _, err := thumbnailContext(context.Background(), data, 0, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
			if result != want {
				t.Fatal("consumer thumbnail bytes changed")
			}
		})
	}
}

// Verify preview opacity includes unsampled pixels
func TestPreviewOpacityIncludesUnsampledPixels(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 640, 320))
	for y := range 320 {
		for x := range 640 {
			source.SetNRGBA(x, y, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
		}
	}
	// A320x160 nearest-neighbor output does not select this source pixel
	source.SetNRGBA(1, 1, color.NRGBA{R: 20, G: 40, B: 60, A: 0})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := thumbnailContext(context.Background(), encoded.Bytes(), 320, 160, true); err == nil {
		t.Fatal("partially transparent preview accepted")
	}
}
