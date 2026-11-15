package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/network-vpn"
)

// main starts the network vpn plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
