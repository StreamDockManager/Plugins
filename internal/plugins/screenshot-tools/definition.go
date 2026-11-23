package screenshottools

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/clipboard", "icons/crop", "icons/display", "icons/window"}, Slug: "screenshot-tools", Name: "Screenshot Tools", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"display", "window", "region", "clipboard"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "display", UpdatedBy: []string{"display"}},
	{Action: "window", UpdatedBy: []string{"window"}},
	{Action: "region", UpdatedBy: []string{"region"}},
	{Action: "clipboard", UpdatedBy: []string{"clipboard"}},
}

var visuals = map[string]engine.Visual{"clipboard": {Icon: "icons/clipboard", States: []string{"icons/clipboard"}}, "display": {Icon: "icons/display", States: []string{"icons/display"}}, "region": {Icon: "icons/crop", States: []string{"icons/crop"}}, "window": {Icon: "icons/window", States: []string{"icons/window"}}}
