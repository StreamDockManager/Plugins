package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

func TestPackageImagesOnlyEncodeDeclaredBoundedArtwork(t *testing.T) {
	root := t.TempDir()
	art := []byte("<svg/>")
	for name, data := range map[string][]byte{"ok.svg": art, "private.svg": art, "big.png": make([]byte, sdk.MaxPortableImageBytes+1)} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	images := newPackageImages(root, Definition{RuntimeAssets: []string{"big.png", "../private.svg", "missing.svg"}}, map[string]Visual{"display": {Icon: "ok"}})
	want, err := sdk.ImageDataURI("image/svg+xml", art)
	if err != nil {
		t.Fatal(err)
	}
	if got := images.image("ok.svg"); got != want {
		t.Fatalf("encoded image = %q", got)
	}
	for _, name := range []string{"private.svg", "../private.svg", "big.png", "missing.svg", want, strings.Repeat("a", 2048)} {
		if images.image(name) != name {
			t.Fatalf("unexpected rewrite of %q", name)
		}
	}
}
