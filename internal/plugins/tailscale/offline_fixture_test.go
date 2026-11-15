package tailscale

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
)

// Create the offline Tailscale fixture using the supplied state path
func New(path string) *engine.Engine { return fixtureengine.New(Definition, path, Factory) }
