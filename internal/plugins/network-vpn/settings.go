package networkvpn

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the network vpn global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected network vpn action
func ActionFields(a string) []settings.Field {
	if a == "reachability" {
		return []settings.Field{metadata.Text("address", "TCP host:port", "1.1.1.1:443")}
	}
	return nil
}
