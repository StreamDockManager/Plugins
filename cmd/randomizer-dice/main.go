package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/randomizer-dice"
)

// main starts the randomizer dice plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
