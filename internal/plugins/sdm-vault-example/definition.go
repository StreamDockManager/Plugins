package vaultexample

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	fields "github.com/StreamDockManager/Plugins/internal/settings"
)

const Slug = "sdm-vault-example"
const WebhookOrigin = "https://127.0.0.1:39481"

var Definition = func() engine.Definition {
	def := engine.Definition{
		Slug: Slug, Name: "SDM Vault Example", Version: "0.1.0", MinimumManager: engine.VaultMinimumManager,
		RequiredCapabilities: []string{"plugin-permissions-v1"},
		Actions:              []string{"permissions", "read", "write", "summary", "location", "webhooks", "plugin-post", "manager-post"},
	}
	// Picker completions arrive after the initiating press.
	for _, action := range def.Actions {
		def.ViewTargets = append(def.ViewTargets, engine.ViewTarget{Action: action, Live: true, UpdatedBy: []string{action}})
	}
	return def
}()

var actions = map[string]struct{ label, icon string }{
	"permissions":  {"Check permission grants", "check"},
	"read":         {"Read a setup slot", "shield"},
	"write":        {"Write a private secret", "save"},
	"summary":      {"Review a clipboard summary", "clipboard"},
	"location":     {"Read a shared location", "globe"},
	"webhooks":     {"Start webhook viewer", "list"},
	"plugin-post":  {"Send from plugin", "network"},
	"manager-post": {"Send through Manager", "globe"},
}

func ActionName(action string) string { return actions[action].label }

func VisualFor(action string) engine.Visual {
	metadata, ok := actions[action]
	if !ok {
		return engine.Visual{}
	}
	icon := "icons/" + metadata.icon
	return engine.Visual{Icon: icon, States: []string{icon}}
}

func PluginFields() []settings.Field { return nil }
func ActionFields(action string) []settings.Field {
	switch action {
	case "read":
		return []settings.Field{fields.Select("kind", "Setup slot", "text", "text", "api-key", "bearer-token", "credential"), fields.Text("display_text", "Display text (optional, Text slot only)", "")}
	case "plugin-post", "manager-post":
		return []settings.Field{fields.Select("transport", "Webhook transport", "https", "https", "http")}
	case "write":
		return []settings.Field{fields.Select("kind", "Private secret kind", "api-key", "api-key", "bearer-token", "credential"), fields.Text("expected_revision", "Current revision (leave empty to create)", "")}
	case "summary":
		f := []settings.Field{fields.Select("operation", "Vault operation", "pick for use", "pick for use", "use selection", "use setup slot", "approve for browsing", "list approved names"), fields.Text("reference", "Selected Vault reference", "")}
		return append(f, fields.Select("kind", "Broker setup slot", "api-key", "api-key", "bearer-token"))
	}
	return nil
}
