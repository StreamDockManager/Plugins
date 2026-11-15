package randomizerdice

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/cards", "icons/dice", "icons/list", "icons/range", "icons/reset"}, Slug: "randomizer-dice", Name: "Randomizer & Dice", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"dice", "range", "list", "draw", "reset"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "dice", UpdatedBy: []string{"dice"}},
	{Action: "range", UpdatedBy: []string{"range"}},
	{Action: "list", UpdatedBy: []string{"list"}},
	{Action: "draw", UpdatedBy: []string{"draw"}},
	{Action: "reset", UpdatedBy: []string{"reset"}},
}

var visuals = map[string]engine.Visual{"dice": {Icon: "icons/dice", States: []string{"icons/dice"}}, "draw": {Icon: "icons/cards", States: []string{"icons/cards"}}, "list": {Icon: "icons/list", States: []string{"icons/list"}}, "range": {Icon: "icons/range", States: []string{"icons/range"}}, "reset": {Icon: "icons/reset", States: []string{"icons/reset"}}}
