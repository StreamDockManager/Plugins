package screenshottools

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the screenshot tools global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected screenshot tools action
func ActionFields(a string) []settings.Field {
	if a == "clipboard" {
		return nil
	}
	return []settings.Field{metadata.Path("path", "Output file", "", "save"), metadata.Select("format", "Image format", "png", "png", "jpg")}
}
