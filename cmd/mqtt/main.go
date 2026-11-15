package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/mqtt"
)

// main starts the mqtt plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
