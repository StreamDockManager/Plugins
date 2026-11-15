package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/http-webhooks"
)

// main starts the http webhooks plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
