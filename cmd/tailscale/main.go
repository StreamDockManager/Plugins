package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/tailscale"
)

// main starts the tailscale plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
