package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/screenshot-tools"
)

// main starts the screenshot tools plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
