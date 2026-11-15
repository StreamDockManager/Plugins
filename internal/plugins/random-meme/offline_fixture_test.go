package randommeme

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
)

// Create the offline random meme fixture using the supplied state path
func New(path string) *engine.Engine { return fixtureengine.New(Definition, path, Factory) }
