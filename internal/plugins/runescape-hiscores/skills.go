package runescapehiscores

import (
	_ "embed"
	"github.com/StreamDockManager/Plugins/internal/hiscores"
)

//go:embed skills.json
var skillsJSON []byte

var Game = hiscores.LoadGame("runescape", skillsJSON)
