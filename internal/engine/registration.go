package engine

import "github.com/StreamDockManager/Plugin-SDK/Go/settings"

// Registration assembles feature-owned definitions and fresh settings factories
type Registration struct {
	Definition   Definition
	PluginFields func() []settings.Field
	ActionFields func(string) []settings.Field
}
