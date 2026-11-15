package systemmonitor

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/cpu", "icons/disk", "icons/memory", "icons/network"}, Slug: "system-monitor", Name: "System Monitor", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1", "telemetry-v1"}, Actions: []string{"cpu", "memory", "disk", "network"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "cpu", UpdatedBy: []string{"cpu"}, Live: true},
	{Action: "memory", UpdatedBy: []string{"memory"}, Live: true},
	{Action: "disk", UpdatedBy: []string{"disk"}, Live: true},
	{Action: "network", UpdatedBy: []string{"network"}, Live: true},
}

var visuals = map[string]engine.Visual{"cpu": {Icon: "icons/cpu", States: []string{"icons/cpu"}}, "disk": {Icon: "icons/disk", States: []string{"icons/disk"}}, "memory": {Icon: "icons/memory", States: []string{"icons/memory"}}, "network": {Icon: "icons/network", States: []string{"icons/network"}}}
