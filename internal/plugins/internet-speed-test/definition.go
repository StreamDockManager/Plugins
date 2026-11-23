package internetspeedtest

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/busy", "icons/cancel", "icons/download", "icons/latency", "icons/request", "icons/upload"}, Slug: "internet-speed-test", Name: "Internet Speed Test", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"run", "cancel", "latency", "download", "upload"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "run", UpdatedBy: []string{"run", "cancel"}, Live: true, AfterActivation: true},
	{Action: "cancel", UpdatedBy: []string{"cancel"}},
	{Action: "latency", UpdatedBy: []string{"latency", "run", "cancel"}, Live: true},
	{Action: "download", UpdatedBy: []string{"download", "run", "cancel"}, Live: true},
	{Action: "upload", UpdatedBy: []string{"upload", "run", "cancel"}, Live: true},
}

var visuals = map[string]engine.Visual{"cancel": {Icon: "icons/cancel", States: []string{"icons/cancel"}}, "download": {Icon: "icons/download", States: []string{"icons/download"}}, "latency": {Icon: "icons/latency", States: []string{"icons/latency"}}, "run": {Icon: "icons/request", States: []string{"icons/request"}}, "upload": {Icon: "icons/upload", States: []string{"icons/upload"}}}
