package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StreamDockManager/Plugin-SDK/Go"
)

// TestAllOfficialIconThemesHaveNoPaintedBackplate verifies all official icon themes have no painted backplate
func TestAllOfficialIconThemesHaveNoPaintedBackplate(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			assets := PluginAssets(definition)
			for name, data := range assets {
				if !strings.HasSuffix(name, ".svg") {
					continue
				}
				svg := string(data)
				if strings.Contains(svg, `<rect width="32" height="32"`) || strings.Contains(svg, `<rect width="64" height="64"`) {
					t.Fatalf("%s paints a background over the Manager background", name)
				}
				if !strings.Contains(svg, "<svg") || !strings.Contains(svg, "</svg>") {
					t.Fatalf("invalid SVG %s", name)
				}
			}
			for _, action := range definition.Actions {
				capability := Descriptor(definition).Actions[ID(definition.Slug)+"."+action]
				for _, icon := range append(capability.States, capability.Icon) {
					for _, path := range []string{icon.Light, icon.Dark} {
						if len(assets[path]) == 0 {
							t.Fatalf("%s has no themed asset %s", action, path)
						}
					}
				}
			}
		})
	}
}

// TestCommonDisplayExposesFullTitleGroupWithoutChangingLegacyRows verifies common display exposes full title group without changing legacy rows
func TestCommonDisplayExposesFullTitleGroupWithoutChangingLegacyRows(t *testing.T) {
	for _, wide := range []bool{false, true} {
		var layout struct {
			Items   []map[string]any `json:"items"`
			Manager struct {
				Groups []struct {
					Key    string   `json:"key"`
					Source string   `json:"source"`
					Keys   []string `json:"keys"`
					Rect   [4]int   `json:"rect"`
				} `json:"text_groups"`
			} `json:"streamDockManager"`
		}
		if err := json.Unmarshal(displayLayout(wide), &layout); err != nil {
			t.Fatal(err)
		}
		if len(layout.Items) != 5 || len(layout.Manager.Groups) != 1 {
			t.Fatalf("missing logical group or legacy items: %+v", layout)
		}
		group := layout.Manager.Groups[0]
		if group.Key != "live_text" || group.Source != "title" || strings.Join(group.Keys, ",") != "line0,line1,line2" || group.Rect[2] == 0 {
			t.Fatalf("invalid full-title group: %+v", group)
		}
	}
}

// TestRetiredPluginsAreNotOffered verifies retired plugins are not offered
func TestRetiredPluginsAreNotOffered(t *testing.T) {
	retired := map[string]bool{"discord": true, "discord-controls": true, "calculator-converter": true, "color-tools": true, "window-management": true, "soundboard": true, "audio-mixer": true, "display-controls": true, "github-actions": true, "mpd": true, "rss-reader": true, "syncthing": true}
	for _, definition := range Definitions {
		if retired[definition.Slug] {
			t.Fatalf("retired plugin is still in the official catalogue: %s", definition.Slug)
		}
	}
}

// TestOfficialAssetAndSettingsCoverage verifies official asset and settings coverage
func TestOfficialAssetAndSettingsCoverage(t *testing.T) {
	if len(Definitions) != 24 {
		t.Fatal("expected 24 plugins", len(Definitions))
	}
	seen := map[string]string{}
	for _, definition := range Definitions {
		if prior := seen[packageGlyphs[definition.Slug]]; prior != "" {
			t.Fatalf("duplicate package geometry: %s / %s", prior, definition.Slug)
		}
		seen[packageGlyphs[definition.Slug]] = definition.Slug
		if packageGlyphs[definition.Slug] == "" {
			t.Fatal("missing package geometry", definition.Slug)
		}
		assets := PluginAssets(definition)
		descriptor := Descriptor(definition)
		if descriptor.ID != "app.streamdockmanager.plugins."+definition.Slug {
			t.Fatal("official plugin namespace mismatch", descriptor.ID)
		}
		for _, asset := range []sdk.ThemedAsset{descriptor.Icon} {
			if len(assets[asset.Light]) == 0 || len(assets[asset.Dark]) == 0 {
				t.Fatal("missing package theme", definition.Slug)
			}
		}
		for actionID, action := range descriptor.Actions {
			if !strings.HasPrefix(actionID, descriptor.ID+".") {
				t.Fatal("action namespace differs from plugin", actionID)
			}
			if len(assets[action.Icon.Light]) == 0 || len(assets[action.Icon.Dark]) == 0 {
				t.Fatal("missing action theme", definition.Slug)
			}
			for _, state := range action.States {
				if len(assets[state.Light]) == 0 || len(assets[state.Dark]) == 0 {
					t.Fatal("missing state theme", definition.Slug)
				}
			}
		}
		if definition.Version != "0.1.0" || definition.MinimumManager != "0.1.0" {
			t.Fatalf("%s must retain the unreleased 0.1.0 baseline", definition.Slug)
		}
	}
	if len(ActionFields("battery-power", "status")) != 0 || len(PluginFields("battery-power")) != 0 || len(PluginFields("weather")) == 0 {
		t.Fatal("functional settings scopes inaccurate")
	}
}
