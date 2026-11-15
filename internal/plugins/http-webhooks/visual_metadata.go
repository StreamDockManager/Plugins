package httpwebhooks

import "github.com/StreamDockManager/Plugins/internal/engine"

// VisualFor returns an independent copy of the same action artwork used at runtime
func VisualFor(action string) engine.Visual { return engine.CloneVisual(visuals[action]) }
