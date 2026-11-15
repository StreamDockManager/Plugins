package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
)

// main starts the random meme plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
