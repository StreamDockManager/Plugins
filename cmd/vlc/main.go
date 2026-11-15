package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/vlc"
)

// main starts the vlc plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
