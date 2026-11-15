package microphonecontrols

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the microphone controls global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected microphone controls action
func ActionFields(a string) []settings.Field {
	mic := settingsDevice("device", "Microphone", "default")
	return []settings.Field{mic}
}

// Build a discoverable device-choice settings field
func settingsDevice(key, label, value string) settings.Field {
	f := metadata.Text(key, label, value)
	f.Type = "device"
	return f
}
