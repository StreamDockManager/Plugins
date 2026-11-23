package integration

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	example "github.com/StreamDockManager/Plugins/internal/plugins/sdm-vault-example"
)

func init() {
	vaultExampleFactory = func(path string) *engine.Engine {
		return fixtureengine.New(example.Definition, path, example.Factory)
	}
}
