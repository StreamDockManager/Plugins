package microphonecontrols

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

// WithMicrophoneAdapter keeps the production lifecycle and scheduler while
// supplying an explicit capture-input adapter for disposable acceptance hosts
func WithMicrophoneAdapter(adapter microphone.Adapter) func(*engine.Engine) engine.Feature {
	return func(base *engine.Engine) engine.Feature {
		plugin := makePlugin(base)
		plugin.mic = &microphone.Controller{Adapter: adapter}
		return plugin.hooks()
	}
}
