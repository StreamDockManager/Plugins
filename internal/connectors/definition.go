package connectors

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"strings"
)

// Define constructs a connector-backed plugin definition
func Define(slug, name string, icons map[string]string, actions ...string) (engine.Definition, map[string]engine.Visual) {
	d := engine.Definition{Slug: slug, Name: name, Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: actions}
	visuals := map[string]engine.Visual{}
	for _, a := range actions {
		states := strings.Split(icons[a], "|")
		for i := range states {
			states[i] = "icons/" + states[i]
		}
		visuals[a] = engine.Visual{Icon: states[0], States: states}
		d.ViewTargets = append(d.ViewTargets, engine.ViewTarget{Action: a, UpdatedBy: append([]string(nil), actions...), Live: true})
	}
	return d, visuals
}
