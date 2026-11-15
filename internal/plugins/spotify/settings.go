package spotify

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the Spotify global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected Spotify action
func ActionFields(a string) []settings.Field {
	if a == "volume" {
		return []settings.Field{metadata.Numeric("step", "Volume percent per dial tick", 1, 1, 100)}
	}
	if a == "seek" {
		return []settings.Field{metadata.Numeric("step", "Seconds per dial tick", 5, 0.1, 3600)}
	}
	return nil
}
