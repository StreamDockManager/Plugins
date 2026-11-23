package randomizerdice

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: dieIconPaths(), Slug: "randomizer-dice", Name: "Randomizer & Dice", Version: "0.1.1", MinimumManager: engine.NotificationsMinimumManager, RequiredCapabilities: []string{}, Actions: []string{"roll-all", "dice", "range", "list", "draw", "reset", "magic-8-ball", countCounterAction, totalCounterAction, "heads-or-tails"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "heads-or-tails", UpdatedBy: []string{"heads-or-tails"}, Live: true},
	{Action: "roll-all", UpdatedBy: []string{"roll-all"}, Live: true},
	{Action: countCounterAction, UpdatedBy: []string{"dice", countCounterAction}, Live: true},
	{Action: totalCounterAction, UpdatedBy: []string{"dice", totalCounterAction}, Live: true},
	{Action: "magic-8-ball", UpdatedBy: []string{"magic-8-ball"}, Live: true},
	{Action: "dice", UpdatedBy: []string{"dice"}, Live: true},
	{Action: "range", UpdatedBy: []string{"range"}},
	{Action: "list", UpdatedBy: []string{"list"}},
	{Action: "draw", UpdatedBy: []string{"draw"}},
	{Action: "reset", UpdatedBy: []string{"reset"}},
}

var visuals = map[string]engine.Visual{"heads-or-tails": {Icon: "icons/heads-or-tails", States: []string{"icons/heads-or-tails"}}, "roll-all": {Icon: "icons/roll-all", States: []string{"icons/roll-all"}}, totalCounterAction: {Icon: "icons/dice-total-counter", States: []string{"icons/dice-total-counter"}}, countCounterAction: {Icon: "icons/dice-roll-counter", States: []string{"icons/dice-roll-counter"}}, "magic-8-ball": {Icon: "icons/magic-8-ball", States: []string{"icons/magic-8-ball"}}, "dice": {Icon: "icons/dice", States: []string{"icons/dice"}}, "draw": {Icon: "icons/cards", States: []string{"icons/cards"}}, "list": {Icon: "icons/list", States: []string{"icons/list"}}, "range": {Icon: "icons/range", States: []string{"icons/range"}}, "reset": {Icon: "icons/reset", States: []string{"icons/reset"}}}
