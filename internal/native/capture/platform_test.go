package capture

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestSelectedJPEGFormatWritesActualJPEG verifies selected JPEG format writes actual JPEG
func TestSelectedJPEGFormatWritesActualJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.png")
	dst := filepath.Join(dir, "result.jpg")
	f, _ := os.Create(src)
	_ = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	f.Close()
	if err := jpegFile(src, dst); err != nil {
		t.Fatal(err)
	}
	f, _ = os.Open(dst)
	defer f.Close()
	config, format, err := image.DecodeConfig(f)
	if err != nil || format != "jpeg" || config.Width != 3 || config.Height != 2 {
		t.Fatal(config, format, err)
	}
}

// TestClipboardDoesNotDecodeEmptyOutputOrConvertJPEG verifies clipboard does not decode empty output or convert JPEG
func TestClipboardDoesNotDecodeEmptyOutputOrConvertJPEG(t *testing.T) {
	for _, extension := range []string{".png", ".jpg", ".jpeg"} {
		path := filepath.Join(t.TempDir(), "clipboard"+extension)
		calls := 0
		err := screenshot(context.Background(), "clipboard", path, func(_ context.Context, mode, output string) error {
			calls++
			if mode != "clipboard" || output != path {
				t.Fatal(mode, output)
			}
			// screencapture -c can succeed without writing image bytes
			return os.WriteFile(output, nil, 0600)
		})
		if err != nil || calls != 1 {
			t.Fatal(err, calls)
		}
	}
}
