package servicemonitor

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the service monitor global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected service monitor action
func ActionFields(a string) []settings.Field {
	fields := []settings.Field{metadata.Numeric("timeout", "Timeout seconds", 5, 0.1, 120)}
	switch a {
	case "http":
		return append(fields, metadata.Text("url", "HTTP check URL", ""))
	case "tcp":
		return append(fields, metadata.Text("address", "TCP host:port", ""))
	case "service":
		return append(fields, settingsDevice("service", "Service name", ""))
	}
	return fields
}

// Build a discoverable device-choice settings field
func settingsDevice(key, label, value string) settings.Field {
	f := metadata.Text(key, label, value)
	f.Type = "device"
	return f
}
