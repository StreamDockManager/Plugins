package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/service-monitor"
)

// main starts the service monitor plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
