package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
)

// main starts the counter tally plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
