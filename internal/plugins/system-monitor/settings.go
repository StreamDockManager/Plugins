package systemmonitor

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the system monitor global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected system monitor action
func ActionFields(a string) []settings.Field {
	if a == "disk" {
		return []settings.Field{metadata.Path("path", "Disk folder or mount point", "/", "folder")}
	}
	return nil
}
