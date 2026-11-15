package countertally

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the counter tally global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected counter tally action
func ActionFields(a string) []settings.Field {
	shared := metadata.Text("shared_id", "Shared state name", "")
	step := metadata.Numeric("step", "Adjustment step", 1, -1000000, 1000000)
	if a == "reset" {
		return []settings.Field{shared, metadata.Numeric("reset", "Reset value", 0, -1e12, 1e12)}
	}
	return []settings.Field{shared, step}
}
