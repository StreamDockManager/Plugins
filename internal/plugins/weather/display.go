package weather

import (
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"strings"
)

// Weather values stay separate from diagnostics and are stable canvas sources
func (e *Plugin) displayView(id string, status displayStatus) engine.Display {
	view := e.BuildDisplayView(id, status)
	b := e.Bindings[id]
	a := action(b.Action)
	layout := "key"
	if b.Surface != nil && b.Surface.Width > b.Surface.Height*13/10 {
		layout = "wide"
	}
	view.Layout = e.ThemedLayout(fmt.Sprintf("display/weather-%s-%s.json", a, layout))
	view.Feedback = map[string]any{"icon": view.Image}
	parts := strings.Split(status.Text, "\n")
	value := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return "—"
	}
	switch a {
	case "forecast":
		view.Feedback["low"] = value(0)
		view.Feedback["high"] = value(1)
	case "sun":
		view.Feedback["sunrise"] = value(0)
		view.Feedback["sunset"] = value(1)
	default:
		view.Feedback["temperature"] = value(0)
	}
	return view
}
