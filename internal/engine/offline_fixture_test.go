package engine

import "github.com/StreamDockManager/Plugin-SDK/Go/authoring"

// New constructs the fixture or runtime instance with its dependencies
func New(def Definition, path string, factory Factory) *Engine {
	return authoring.New(def, path, PresentedFactory(def, factory))
}
