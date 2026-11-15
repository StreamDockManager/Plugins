package runescapehiscores

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/hiscores"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"time"
)

var Icons = map[string]string{"overall": "skill-cape", "skill": "skill-cape", "activity": "check", "xp-change": "plus-minus", "reset-baseline": "reset", "refresh": "cycle"}
var Definition, visuals = c.Define("runescape-hiscores", "RuneScape Hiscores", Icons, "overall", "skill", "activity", "xp-change", "reset-baseline", "refresh")

// Declare the hiscores artwork required by packaged game displays
func init() { Definition.RuntimeAssets = Game.RuntimeAssets() }

// Declare the RuneScape hiscores global settings fields
func PluginFields() []settings.Field { return []settings.Field{} }

// Declare settings supported by the selected RuneScape hiscores action
func ActionFields(a string) []settings.Field {
	return Game.ActionFields(a, "normal", "ironman", "hardcore")
}

var Boards = map[string]string{"normal": "https://secure.runescape.com/m=hiscore/index_lite.json", "ironman": "https://secure.runescape.com/m=hiscore_ironman/index_lite.json", "hardcore": "https://secure.runescape.com/m=hiscore_hardcore_ironman/index_lite.json"}
var LegacyBoards = map[string]string{"normal": "https://secure.runescape.com/m=hiscore/index_lite.ws", "ironman": "https://secure.runescape.com/m=hiscore_ironman/index_lite.ws", "hardcore": "https://secure.runescape.com/m=hiscore_hardcore_ironman/index_lite.ws"}

// The lite protocol places Overall and the 29 skills before two-column
// activities. Fail closed when Jagex adds/removes a row or changes a row shape
// Activity identifiers are deliberately positional; obsolete activities remain
// in the API and are not silently relabelled as newer game content
// Verified against Jagex's JSON and lite responses on 2026-10-30 UTC. The two
// removed activities (15/16) are absent from CSV; preserve the provider's IDs
var Mapping = hiscores.Mapping{Skills: []string{"Overall", "Attack", "Defence", "Strength", "Hitpoints", "Ranged", "Prayer", "Magic", "Cooking", "Woodcutting", "Fletching", "Fishing", "Firemaking", "Crafting", "Smithing", "Mining", "Herblore", "Agility", "Thieving", "Slayer", "Farming", "Runecraft", "Hunter", "Construction", "Summoning", "Dungeoneering", "Divination", "Invention", "Archaeology", "Necromancy"},
	Activities:  []string{"Bounty Hunters", "Bounty Hunter Rogues", "Dominion Tower", "The Crucible", "Castle Wars Games", "B.A Attackers", "B.A Defenders", "B.A Collectors", "B.A Healers", "Duel Tournament", "Mobilising Armies", "Conquest", "Fist of Guthix", "GG: Resource Race", "GG: Athletics", "WE2 Armadyl lifetime contribution", "WE2 Bandos lifetime contribution", "WE2 Armadyl PvP kills", "WE2 Bandos PvP kills", "Heist: Guard Prestige", "Heist: Robber Prestige", "CFP: 5 game average", "Cow tips", "Rats Slaughtered", "RuneScore", "Clue Scrolls (easy)", "Clue Scrolls (medium)", "Clue Scrolls (hard)", "Clue Scrolls (elite)", "Clue Scrolls (master)", "Leagues: CATALYST", "Leagues II: Equilibrium"},
	ActivityIDs: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33},
}

var Factory = hiscores.Factory(Game, c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 5 * time.Minute, New: func(chan<- struct{}) c.Driver {
	d := hiscores.New(Boards, &Mapping)
	d.Game = &Game
	d.FallbackBoards = LegacyBoards
	return d
}})
