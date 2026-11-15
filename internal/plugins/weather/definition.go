package weather

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/cloud-sun", "icons/forecast", "icons/sun"}, Slug: "weather", Name: "Weather", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"conditions", "forecast", "sun"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "conditions", UpdatedBy: []string{"conditions"}, Live: true},
	{Action: "forecast", UpdatedBy: []string{"forecast"}, Live: true},
	{Action: "sun", UpdatedBy: []string{"sun"}, Live: true},
}

var visuals = map[string]engine.Visual{"conditions": {Icon: "icons/cloud-sun", States: []string{"icons/cloud-sun"}}, "forecast": {Icon: "icons/forecast", States: []string{"icons/forecast"}}, "sun": {Icon: "icons/sun", States: []string{"icons/sun"}}}
