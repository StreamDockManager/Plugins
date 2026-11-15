package countertally

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/minus", "icons/plus", "icons/reset"}, Slug: "counter-tally", Name: "Counter & Tally", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"increment", "decrement", "reset"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "increment", UpdatedBy: []string{"increment", "decrement", "reset"}, Shared: true, Live: true},
	{Action: "decrement", UpdatedBy: []string{"decrement", "increment", "reset"}, Shared: true, Live: true},
	{Action: "reset", UpdatedBy: []string{"reset"}},
}

var visuals = map[string]engine.Visual{"decrement": {Icon: "icons/minus", States: []string{"icons/minus"}}, "increment": {Icon: "icons/plus", States: []string{"icons/plus"}}, "reset": {Icon: "icons/reset", States: []string{"icons/reset"}}}
