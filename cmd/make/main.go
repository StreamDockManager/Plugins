package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/make"
)

// main runs the make command
func main() { engine.Main(plugin.Definition, plugin.Factory) }
