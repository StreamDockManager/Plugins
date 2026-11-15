package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/battery-power"
)

// main starts the battery power plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
