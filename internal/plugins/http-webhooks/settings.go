package httpwebhooks

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the HTTP webhook global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected HTTP webhook action
func ActionFields(a string) []settings.Field {
	shared := metadata.Text("shared_id", "Shared state name", "")
	return []settings.Field{shared, metadata.Select("method", "HTTP method", "GET", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"), metadata.Text("url", "Request URL", ""), settingsPairs("headers", "Headers (environment references supported)"), settingsLines("body", "Request body"), metadata.Numeric("timeout", "Timeout seconds", 10, 0.1, 120), settingsPairs("fields", "Named response fields → extraction paths"), metadata.Text("field", "Displayed field name", "")}
}

// Build a multiline settings field with the supplied label
func settingsLines(key, label string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "textarea"}
}

// Build the structured key-value settings field
func settingsPairs(key, label string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "pairs"}
}
