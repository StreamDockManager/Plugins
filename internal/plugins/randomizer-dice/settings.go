package randomizerdice

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

const defaultListItems = "Yes\nNo"

// Declare the randomizer dice global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected randomizer dice action
func ActionFields(a string) []settings.Field {
	shared := metadata.Text("shared_id", "Shared state name", "")
	shared.Label = "Shared state name (match Draw and Reset)"
	switch a {
	case "heads-or-tails", "roll-all", "magic-8-ball", countCounterAction, totalCounterAction:
		return nil
	case "dice":
		badge := metadata.Check("show_result_badge", "Show result badge")
		badge.Default = true
		return []settings.Field{shared, metadata.Numeric("sides", "Sides", 6, 2, 1000000), badge}
	case "range":
		return []settings.Field{shared, metadata.Numeric("min", "Minimum", 1, -1e9, 1e9), metadata.Numeric("max", "Maximum", 100, -1e9, 1e9)}
	case "list":
		items := settingsLines("items", "Items (one per line)")
		items.Default = defaultListItems
		items.Required = true
		return []settings.Field{shared, items}
	case "draw":
		return []settings.Field{shared, settingsLines("items", "Items (one per line; empty uses a 52-card deck)")}
	default:
		return []settings.Field{shared}
	}
}

// Build a multiline settings field with the supplied label
func settingsLines(key, label string) settings.Field {
	return settings.Field{Key: key, Label: label, Type: "textarea"}
}

// Missing or malformed preferences retain the established default-on behavior.
// Only a JSON boolean false disables the small-die result badge.
func showResultBadge(settings map[string]any) bool {
	value, valid := settings["show_result_badge"].(bool)
	return !valid || value
}
