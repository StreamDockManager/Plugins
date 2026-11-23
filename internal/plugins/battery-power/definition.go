package batterypower

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/battery"}, Slug: "battery-power", Name: "Battery & Power", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"status"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "status", UpdatedBy: []string{"status"}, Live: true},
}

var visuals = map[string]engine.Visual{"status": {Icon: "icons/battery", States: []string{"icons/battery"}}}
