package catalog

import (
	"encoding/json"
	"strings"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

var paletteJSON = engine.PaletteJSON()
var palettes = func() map[string]map[string]string {
	var p struct {
		Themes map[string]map[string]string `json:"themes"`
	}
	_ = json.Unmarshal(paletteJSON, &p)
	return p.Themes
}()

// themed constructs the paired light and dark artwork paths
func themed(path string) sdk.ThemedAsset {
	return sdk.ThemedAsset{Light: path + "-light.svg", Dark: path + "-dark.svg"}
}

// Descriptor builds plugin capabilities from the registered actions and settings
func Descriptor(d Definition) sdk.Capabilities {
	result := sdk.Capabilities{MinimumManager: d.MinimumManager, RequiredCapabilities: d.RequiredCapabilities, Version: 1, ID: ID(d.Slug), PluginVersion: d.Version, ThemeProtocol: 1, SettingsProtocol: 1, Icon: themed("icon"), Actions: map[string]sdk.ActionCapability{}}
	if len(PluginFields(d.Slug)) > 0 {
		result.PluginSettings = "inspector.html"
	}
	if d.Slug == "random-meme" {
		result.InputProtocol = 1
	}
	for _, a := range d.Actions {
		v := VisualFor(d.Slug, a)
		layout := "display/wide.json"
		if d.Slug == "random-meme" {
			layout = "display/meme.json"
		}
		if d.Slug == "weather" {
			layout = "display/weather-" + a + "-wide.json"
		}
		if d.Slug == "spotify" {
			layout = "spotify/control.json"
			if a == "status" {
				layout = "spotify/status-wide.json"
			}
		}
		ac := sdk.ActionCapability{AdditiveDial: engine.AdditiveDial(d.Slug, a), Icon: themed(v.Icon), States: []sdk.ThemedAsset{}, FeedbackLayout: layout, ControlTypes: []string{"button", "encoder", "touch"},
			Refresh: engine.RefreshPolicy(d.Slug, a)}
		if buttonFirstControls(d.Slug) {
			ac.ControlTypes = []string{"button", "touch"}
			if (d.Slug == "vlc" && (a == "volume" || a == "seek")) || (d.Slug == "obs-studio" && a == "volume") || (d.Slug == "home-assistant" && a == "brightness") {
				ac.ControlTypes = []string{"encoder"}
			}
		}
		if d.Slug == "spotify" {
			switch a {
			case "volume", "seek":
				ac.ControlTypes = []string{"encoder"}
				ac.FeedbackLayout = "spotify/volume-display.json"
				if a == "seek" {
					ac.FeedbackLayout = "spotify/progress.json"
				}
			case "volume-display", "progress", "track-info":
				ac.ControlTypes = []string{"button", "touch"}
				ac.FeedbackLayout = "spotify/" + a + ".json"
			}
		}
		fields := ActionFields(d.Slug, a)
		for _, field := range fields {
			ac.ParameterFields = append(ac.ParameterFields, field.Key)
		}
		if len(fields) > 0 {
			ac.Settings = "inspector.html"
		}
		for _, state := range v.States {
			ac.States = append(ac.States, themed(state))
		}
		result.Actions[ID(d.Slug)+"."+a] = ac
	}
	return result
}

// ThemeAsset resolves the theme-specific asset path
func ThemeAsset(path, theme string) string {
	ext := ".svg"
	if !strings.HasSuffix(path, ext) {
		return path
	}
	return strings.TrimSuffix(path, ext) + "-" + theme + ext
}

// Control capability classification is independent of settings ownership
func buttonFirstControls(slug string) bool {
	switch slug {
	case "obs-studio", "home-assistant", "random-meme", "vlc", "tailscale", "mqtt", "make", "osrs-hiscores", "runescape-hiscores", "retroachievements":
		return true
	}
	return false
}
