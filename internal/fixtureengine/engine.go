// Package fixtureengine constructs explicit disposable file-backed runtimes for tests and audit tools
// Production entrypoints must use engine.Main and host-managed storage
package fixtureengine

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// New constructs the fixture or runtime instance with its dependencies
func New(def engine.Definition, path string, factory engine.Factory) *engine.Engine {
	return authoring.New(def, path, engine.PresentedFactory(def, factory))
}
