package main

import (
	official "github.com/StreamDockManager/Plugins/internal/catalog"
	"testing"
)

// TestSupportedPackageMatrixAndExplicitRequests verifies supported package matrix and explicit requests
func TestSupportedPackageMatrixAndExplicitRequests(t *testing.T) {
	builder := testRunner(t)
	builder.selected, builder.planSelected = "", nil
	for platform, count := range map[string]int{"linux": 23, "windows": 24, "darwin": 24} {
		definitions, err := builder.definitionsFor(platform)
		if err != nil || len(definitions) != count {
			t.Fatal(platform, len(definitions), err)
		}
		for _, d := range definitions {
			if !official.Supports(d.Slug, platform) {
				t.Fatal(d.Slug, platform)
			}
		}
	}
	for _, slug := range []string{"audio-mixer", "display-controls", "github-actions", "mpd", "rss-reader", "syncthing"} {
		builder.selected = slug
		if _, err := builder.definitionsFor("windows"); err == nil {
			t.Fatalf("retired plugin %s still accepts an explicit build", slug)
		}
	}
	builder.selected = "screenshot-tools"
	if _, err := builder.definitionsFor("linux"); err == nil {
		t.Fatal("explicit unsupported build accepted")
	}
	if ds, err := builder.definitionsFor("darwin"); err != nil || len(ds) != 1 {
		t.Fatal(ds, err)
	}
	builder.selected = ""
	builder.planSelected = map[string]bool{official.ID("screenshot-tools"): true, official.ID("counter-tally"): true}
	if ds, err := builder.definitionsFor("linux"); err != nil || len(ds) != 1 || ds[0].Slug != "counter-tally" {
		t.Fatal(ds, err)
	}
}
