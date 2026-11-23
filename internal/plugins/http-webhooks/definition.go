package httpwebhooks

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/busy", "icons/cancel", "icons/globe", "icons/latency", "icons/list", "icons/request"}, Slug: "http-webhooks", Name: "HTTP & Webhooks", Version: "0.1.1", MinimumManager: engine.VaultMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"request", "status", "duration", "field", "cancel"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "request", UpdatedBy: []string{"request"}, Live: true},
	{Action: "status", UpdatedBy: []string{"status"}, Live: true},
	{Action: "duration", UpdatedBy: []string{"duration"}, Live: true},
	{Action: "field", UpdatedBy: []string{"field"}, Live: true},
	{Action: "cancel", UpdatedBy: []string{"cancel"}},
}

var visuals = map[string]engine.Visual{"cancel": {Icon: "icons/cancel", States: []string{"icons/cancel"}}, "duration": {Icon: "icons/latency", States: []string{"icons/latency"}}, "field": {Icon: "icons/list", States: []string{"icons/list"}}, "request": {Icon: "icons/request", States: []string{"icons/request"}}, "status": {Icon: "icons/globe", States: []string{"icons/globe"}}}
