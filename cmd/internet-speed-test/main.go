package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/internet-speed-test"
)

// main starts the internet speed test plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
