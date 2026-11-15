package networkvpn

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
)

// Create the offline network vpn fixture using the supplied state path
func New(path string) *Plugin {
	var p *Plugin
	fixtureengine.New(Definition, path, func(base *engine.Engine) engine.Feature { p = makePlugin(base); return p.hooks() })
	return p
}
