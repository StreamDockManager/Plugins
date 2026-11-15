package steamlibrary

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/bookmark", "icons/busy", "icons/cycle", "icons/latency", "icons/next", "icons/play", "icons/previous"}, Slug: "steam-library", Name: "Steam Library", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"launch", "next", "previous", "favorite", "refresh", "playtime"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "launch", UpdatedBy: []string{"launch"}},
	{Action: "next", UpdatedBy: []string{"next"}},
	{Action: "previous", UpdatedBy: []string{"previous"}},
	{Action: "favorite", UpdatedBy: []string{"favorite"}},
	{Action: "refresh", UpdatedBy: []string{"refresh"}},
	{Action: "playtime", UpdatedBy: []string{"playtime"}},
}

var visuals = map[string]engine.Visual{"favorite": {Icon: "icons/bookmark", States: []string{"icons/bookmark"}}, "launch": {Icon: "icons/play", States: []string{"icons/play"}}, "next": {Icon: "icons/next", States: []string{"icons/next"}}, "playtime": {Icon: "icons/latency", States: []string{"icons/latency"}}, "previous": {Icon: "icons/previous", States: []string{"icons/previous"}}, "refresh": {Icon: "icons/cycle", States: []string{"icons/cycle"}}}
