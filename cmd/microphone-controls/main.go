package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
)

// main starts the microphone controls plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
