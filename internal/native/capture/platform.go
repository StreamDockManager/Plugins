package capture

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"strings"

	"os"
	"path/filepath"
)

// Screenshot captures an image with the configured output options
func Screenshot(ctx context.Context, mode, path string) error {
	return screenshot(ctx, mode, path, Capture)
}

// screenshot captures and prepares the requested screenshot payload
func screenshot(ctx context.Context, mode, path string, capture func(context.Context, string, string) error) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("capture path must be absolute")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	if mode == "clipboard" {
		return capture(ctx, mode, path)
	}
	if strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg") {
		temp, err := os.CreateTemp(filepath.Dir(path), ".capture-*.png")
		if err != nil {
			return err
		}
		name := temp.Name()
		temp.Close()
		defer os.Remove(name)
		if err = capture(ctx, mode, name); err != nil {
			return err
		}
		return jpegFile(name, path)
	}
	return capture(ctx, mode, path)
}

// jpegFile encodes the captured image as a bounded JPEG file
func jpegFile(source, destination string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(destination), ".capture-*.jpg")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if err = jpeg.Encode(out, img, &jpeg.Options{Quality: 92}); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), destination)
}
