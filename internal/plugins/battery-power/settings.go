package batterypower

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
)

// Declare the battery power global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected battery power action
func ActionFields(a string) []settings.Field { return nil }
