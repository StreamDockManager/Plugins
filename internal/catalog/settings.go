package catalog

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

type Field = settings.Field

// PluginFields returns settings fields shared by the plugin
func PluginFields(slug string) []Field {
	if fields := settingsRegistry[slug].PluginFields; fields != nil {
		return fields()
	}
	return nil
}

// actionFields builds settings fields for the selected action
func actionFields(slug, action string) []Field {
	if fields := settingsRegistry[slug].ActionFields; fields != nil {
		return fields(action)
	}
	return nil
}

// ActionFields returns the public settings schema for the action
func ActionFields(slug, a string) []Field {
	fields := actionFields(slug, a)
	if slug == "http-webhooks" || slug == "service-monitor" {
		if policy := engine.RefreshPolicy(slug, a); policy != nil {
			fields = append(fields, metadata.Numeric("update_interval_ms", "Automatic read interval in milliseconds (0 loads once)", float64(policy.DefaultMS), 0, float64(policy.MaximumMS)))
		}
	}
	return fields
}

// SettingsModule serializes the inspector settings module
func SettingsModule(d Definition) any {
	actions := map[string][]Field{}
	refresh := map[string]any{}
	for _, a := range d.Actions {
		if policy := engine.RefreshPolicy(d.Slug, a); policy != nil {
			refresh[ID(d.Slug)+"."+a] = policy
		}
		actions[ID(d.Slug)+"."+a] = ActionFields(d.Slug, a)
	}
	return map[string]any{"version": 1, "name": d.Name, "plugin": PluginFields(d.Slug), "actions": actions, "refresh": refresh, "palette": jsonPalette(), "location": d.Slug == "weather", "hiscores": d.Slug == "osrs-hiscores" || d.Slug == "runescape-hiscores", "speedtest": d.Slug == "internet-speed-test", "microphones": d.Slug == "microphone-controls"}
}

// jsonPalette serializes the shared palette for inspector use
func jsonPalette() any { return map[string]any{"version": 1, "themes": palettes} }
