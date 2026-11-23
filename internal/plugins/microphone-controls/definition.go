package microphonecontrols

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/microphone", "icons/microphone-off", "icons/microphone-toggle"}, Slug: "microphone-controls", Name: "Microphone Controls", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"mute", "unmute", "toggle"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "mute", UpdatedBy: []string{"mute"}},
	{Action: "unmute", UpdatedBy: []string{"unmute"}},
	{Action: "toggle", UpdatedBy: []string{"toggle", "mute", "unmute"}, Live: true},
}

var visuals = map[string]engine.Visual{"mute": {Icon: "icons/microphone-off", States: []string{"icons/microphone", "icons/microphone-off"}}, "toggle": {Icon: "icons/microphone-toggle", States: []string{"icons/microphone", "icons/microphone-off"}}, "unmute": {Icon: "icons/microphone", States: []string{"icons/microphone", "icons/microphone-off"}}}
