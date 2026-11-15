package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/steam-library"
)

// main starts the steam library plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
