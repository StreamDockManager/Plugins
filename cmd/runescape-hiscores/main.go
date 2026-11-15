package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
)

// main starts the runescape hiscores plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
