package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/spotify"
)

// main starts the spotify plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
