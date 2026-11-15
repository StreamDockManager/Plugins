package settings

import "github.com/StreamDockManager/Plugin-SDK/Go/settings"

// Text declares a text settings field
func Text(key, label, value string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "text", Default: value}
}

// Sensitive declares a settings field whose contents require redaction
func Sensitive(key, label string) settings.Field {
	f := Text(key, label+" (or ${ENV_NAME})", "")
	f.Sensitive = true
	return f
}

// Device declares a device-selection settings field
func Device(key, label string) settings.Field { f := Text(key, label, ""); f.Type = "device"; return f }

// Path declares a path settings field
func Path(key, label, value, picker string) settings.Field {
	f := Text(key, label, value)
	f.Type = "path"
	f.PathPicker = picker
	return f
}

// Select declares a settings field with named choices
func Select(key, label, value string, options ...string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "select", Default: value, Options: options}
}

// Numeric declares a numeric settings field
func Numeric(key, label string, value, lo, hi float64) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "number", Default: value, Min: &lo, Max: &hi}
}

// Check declares a boolean settings field
func Check(key, label string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "checkbox", Default: false}
}

// Keys extracts the declared settings keys
func Keys(fields []settings.Field) []string {
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.Key)
	}
	return keys
}
