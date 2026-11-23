package catalog

import (
	"slices"
	"testing"
)

// TestRemovedOfficialPluginsAreExplicitlyRetired verifies removed official plugins are explicitly retired
func TestRemovedOfficialPluginsAreExplicitlyRetired(t *testing.T) {
	for _, slug := range []string{"audio-mixer", "display-controls", "github-actions", "mpd", "rss-reader", "syncthing"} {
		if !slices.Contains(Policy().RetiredIDs, ID(slug)) {
			t.Fatalf("%s is missing the explicit catalog retirement", slug)
		}
		for _, platform := range []string{"windows", "linux", "macos", "darwin"} {
			if Supports(slug, platform) {
				t.Fatalf("%s is still packaged for %s", slug, platform)
			}
		}
	}
}

// TestClipboardAndCopyPasteMetadata verifies clipboard and copy paste metadata
func TestClipboardAndCopyPasteMetadata(t *testing.T) {
	if len(ActionFields("screenshot-tools", "clipboard")) != 0 {
		t.Fatal("clipboard exposes file settings")
	}
	if len(ActionFields("screenshot-tools", "display")) == 0 {
		t.Fatal("file capture lost settings")
	}
	if ActionName("random-meme", "send") != "Paste link" || ActionName("random-meme", "copy") != "Copy link" {
		t.Fatal("link labels differ")
	}
	for _, id := range Policy().RetiredIDs {
		for _, d := range Definitions {
			if ID(d.Slug) == id {
				t.Fatal("retired definition", id)
			}
		}
	}
}

// Verify the new Roll All action uses its human-readable name in the packaged action list
func TestRandomizerRollAllActionName(t *testing.T) {
	if got := ActionName("randomizer-dice", "roll-all"); got != "Roll All" {
		t.Fatalf("Roll All action label = %q", got)
	}
}
