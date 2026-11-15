package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/obs-studio"
)

// main starts the obs studio plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
