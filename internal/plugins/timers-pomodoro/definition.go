package timerspomodoro

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/check", "icons/coffee", "icons/reset", "icons/stopwatch", "icons/timer", "icons/timer-adjust", "icons/timer-pause", "icons/tomato"}, Slug: "timers-pomodoro", Name: "Timers & Pomodoro", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"countdown", "stopwatch", "pomodoro", "reset", "adjust"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "countdown", UpdatedBy: []string{"countdown", "stopwatch", "pomodoro", "reset", "adjust"}, Shared: true, Live: true},
	{Action: "stopwatch", UpdatedBy: []string{"stopwatch", "countdown", "pomodoro", "reset", "adjust"}, Shared: true, Live: true},
	{Action: "pomodoro", UpdatedBy: []string{"pomodoro", "countdown", "stopwatch", "reset", "adjust"}, Shared: true, Live: true},
	{Action: "reset", UpdatedBy: []string{"reset"}},
	{Action: "adjust", UpdatedBy: []string{"adjust"}},
}

var visuals = map[string]engine.Visual{"adjust": {Icon: "icons/timer-adjust", States: []string{"icons/timer-adjust"}}, "countdown": {Icon: "icons/timer", States: []string{"icons/timer", "icons/timer-pause", "icons/check", "icons/coffee"}}, "pomodoro": {Icon: "icons/tomato", States: []string{"icons/tomato", "icons/timer-pause", "icons/check", "icons/coffee"}}, "reset": {Icon: "icons/reset", States: []string{"icons/reset"}}, "stopwatch": {Icon: "icons/stopwatch", States: []string{"icons/stopwatch", "icons/timer-pause", "icons/check", "icons/coffee"}}}
