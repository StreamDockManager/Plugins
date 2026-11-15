package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/weather"
)

// main starts the weather plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
