package spotify

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
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
			if max(w, h) > 256 {
				w, h = max(1, w*256/max(cfg.Width, cfg.Height)), max(1, h*256/max(cfg.Width, cfg.Height))
			}
			expected := image.NewRGBA(image.Rect(0, 0, w, h))
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
			result, err := artwork(context.Background(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data))
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
