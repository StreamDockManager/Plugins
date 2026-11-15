package spotify

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, Slug: "spotify", Name: "Spotify", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, RuntimeAssets: []string{"spotify/artwork.json", "spotify/artwork-light.json", "spotify/artwork-dark.json"}, Actions: []string{"toggle", "next", "previous", "volume", "seek", "status", "volume-display", "progress", "track-info"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "toggle", UpdatedBy: []string{"toggle", "next", "previous"}, Live: true},
	{Action: "next", UpdatedBy: []string{"next"}},
	{Action: "previous", UpdatedBy: []string{"previous"}},
	{Action: "volume", UpdatedBy: []string{"volume"}, Live: true},
	{Action: "seek", UpdatedBy: []string{"seek", "toggle", "next", "previous"}, Live: true},
	{Action: "status", UpdatedBy: []string{"status", "toggle", "next", "previous", "seek", "volume"}, Live: true},
	{Action: "volume-display", UpdatedBy: []string{"volume-display", "volume"}, Live: true},
	{Action: "progress", UpdatedBy: []string{"progress", "toggle", "next", "previous", "seek"}, Live: true},
	{Action: "track-info", UpdatedBy: []string{"track-info", "toggle", "next", "previous"}, Live: true},
}

var visuals = map[string]engine.Visual{"next": {Icon: "spotify/next", States: []string{"spotify/next"}}, "previous": {Icon: "spotify/previous", States: []string{"spotify/previous"}}, "seek": {Icon: "spotify/seek", States: []string{"spotify/seek"}}, "status": {Icon: "spotify/status", States: []string{"spotify/status"}}, "toggle": {Icon: "spotify/toggle", States: []string{"spotify/play", "spotify/pause"}}, "volume": {Icon: "spotify/volume", States: []string{"spotify/volume"}}}

// Initialize visual metadata for Spotify live display actions
func init() {
	for _, action := range []string{"volume-display", "progress", "track-info"} {
		visuals[action] = engine.Visual{Icon: "spotify/" + action, States: []string{"spotify/" + action}}
	}
}
