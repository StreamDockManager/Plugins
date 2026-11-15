package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/home-assistant"
)

// main starts the home assistant plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
