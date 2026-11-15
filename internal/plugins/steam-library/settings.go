package steamlibrary

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the steam library global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Path("steam_path", "Steam installation folder", "", "folder"), metadata.Text("account", "Local account ID (optional)", "")}
}

// Declare settings supported by the selected steam library action
func ActionFields(a string) []settings.Field {
	if a == "next" || a == "previous" || a == "launch" {
		return []settings.Field{settingsDevice("app_id", "Steam application ID (empty uses selection)", ""), metadata.Check("favorites_only", "Navigate favorites only")}
	}
	switch a {
	case "playtime", "favorite":
		return []settings.Field{settingsDevice("app_id", "Steam application ID", "")}
	}
	return nil
}

// Build a discoverable device-choice settings field
func settingsDevice(key, label, value string) settings.Field {
	f := metadata.Text(key, label, value)
	f.Type = "device"
	return f
}
