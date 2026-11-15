package internetspeedtest

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the internet speed test global settings fields
func PluginFields() []settings.Field {
	mode := metadata.Select("server_mode", "Server selection", "auto", "auto", "manual")
	mode.OptionLabels = map[string]string{"auto": "Detect server (lowest nearby ping)", "manual": "Choose a server"}
	return []settings.Field{mode, metadata.Text("server", "Speed-test server ID", "")}
}

// Declare settings supported by the selected internet speed test action
func ActionFields(a string) []settings.Field { return nil }
