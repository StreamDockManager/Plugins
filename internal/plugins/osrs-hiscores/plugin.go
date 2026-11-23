package osrshiscores

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/hiscores"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"time"
)

var Icons = map[string]string{"overall": "skill-cape", "skill": "skill-cape", "activity": "check", "xp-change": "plus-minus", "reset-baseline": "reset", "refresh": "cycle"}
var Definition, visuals = c.DefineVault("osrs-hiscores", "OSRS Hiscores", Icons, "overall", "skill", "activity", "xp-change", "reset-baseline", "refresh")

// Declare the hiscores artwork required by packaged game displays
func init() { Definition.RuntimeAssets = Game.RuntimeAssets() }

// Declare the OSRS hiscores global settings fields
func PluginFields() []settings.Field { return []settings.Field{} }

// Declare settings supported by the selected OSRS hiscores action
func ActionFields(a string) []settings.Field {
	return Game.ActionFields(a, "normal", "ironman", "hardcore", "ultimate")
}

var Boards = map[string]string{"normal": "https://secure.runescape.com/m=hiscore_oldschool/index_lite.json", "ironman": "https://secure.runescape.com/m=hiscore_oldschool_ironman/index_lite.json", "hardcore": "https://secure.runescape.com/m=hiscore_oldschool_hardcore_ironman/index_lite.json", "ultimate": "https://secure.runescape.com/m=hiscore_oldschool_ultimate/index_lite.json"}
var Factory = hiscores.Factory(Game, c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 5 * time.Minute, New: func(chan<- struct{}) c.Driver { d := hiscores.New(Boards, nil); d.Game = &Game; return d }})
