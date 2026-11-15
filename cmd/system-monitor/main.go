package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
)

// main starts the system monitor plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
