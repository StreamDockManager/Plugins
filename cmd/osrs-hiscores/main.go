package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
)

// main starts the osrs hiscores plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
