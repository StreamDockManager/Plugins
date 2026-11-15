package servicemonitor

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/busy", "icons/cpu", "icons/cycle", "icons/globe", "icons/network"}, Slug: "service-monitor", Name: "Service Monitor", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"http", "tcp", "service", "refresh"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "http", UpdatedBy: []string{"http", "refresh"}, Live: true},
	{Action: "tcp", UpdatedBy: []string{"tcp", "refresh"}, Live: true},
	{Action: "service", UpdatedBy: []string{"service", "refresh"}, Live: true},
	{Action: "refresh", UpdatedBy: []string{"refresh"}},
}

var visuals = map[string]engine.Visual{"http": {Icon: "icons/globe", States: []string{"icons/globe"}}, "refresh": {Icon: "icons/cycle", States: []string{"icons/cycle"}}, "service": {Icon: "icons/cpu", States: []string{"icons/cpu"}}, "tcp": {Icon: "icons/network", States: []string{"icons/network"}}}
