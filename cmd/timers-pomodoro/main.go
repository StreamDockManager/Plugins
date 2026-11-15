package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	plugin "github.com/StreamDockManager/Plugins/internal/plugins/timers-pomodoro"
)

// main starts the timers pomodoro plugin through the shared runtime
func main() { engine.Main(plugin.Definition, plugin.Factory) }
