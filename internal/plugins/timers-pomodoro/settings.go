package timerspomodoro

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the timers pomodoro global settings fields
func PluginFields() []settings.Field { return nil }

// Declare settings supported by the selected timers pomodoro action
func ActionFields(a string) []settings.Field {
	shared := metadata.Text("shared_id", "Shared state name", "")
	switch a {
	case "reset":
		return []settings.Field{shared}
	case "countdown":
		return []settings.Field{shared, metadata.Numeric("seconds", "Duration in seconds", 300, 1, 604800)}
	case "stopwatch":
		return []settings.Field{shared}
	case "pomodoro":
		return []settings.Field{shared, metadata.Numeric("focus_minutes", "Focus minutes", 25, 0.02, 10080), metadata.Numeric("break_minutes", "Break minutes", 5, 0.02, 10080)}
	case "adjust":
		return []settings.Field{shared, metadata.Numeric("step", "Adjustment seconds", 60, -604800, 604800)}
	}
	return nil
}
