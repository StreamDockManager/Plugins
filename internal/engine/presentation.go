package engine

import (
	"strings"
	"unicode"

	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
)

// PresentedFactory applies official visual policy to an SDK factory
func PresentedFactory(def Definition, factory Factory) Factory {
	return func(e *Engine) Feature {
		f := factory(e)
		// These features own artwork, specialized fields, and saved field identities
		switch def.Slug {
		case "weather", "spotify", "osrs-hiscores", "runescape-hiscores":
			return f
		}
		f.Present = func(id string, status DisplayStatus) Presentation {
			return presentation(def.Slug, Action(e.Bindings[id].Action), status)
		}
		if f.View == nil {
			f.View = func(id string, status DisplayStatus) Display {
				return CoordinatedView(e, id, status)
			}
		}
		return f
	}
}

// CoordinatedView keeps the SDK's feedback keys and full title while giving
// the foreground value more room on the smallest physical keys
func CoordinatedView(e *Engine, id string, status DisplayStatus) Display {
	view := e.BuildDisplayView(id, status)
	surface := e.Bindings[id].Surface
	if surface == nil {
		return view
	}
	return authoring.CompactDisplay(view, surface.Width, surface.Height)
}

// presentation adapts an action view to the shared presentation contract
func presentation(slug, action string, status DisplayStatus) Presentation {
	text := strings.TrimSpace(status.Text)
	if status.Unavailable || strings.HasPrefix(text, "Unavailable") || strings.HasPrefix(text, "Stale") {
		label := "Unavailable"
		if strings.HasPrefix(text, "Stale") {
			lines := strings.Split(text, "\n")
			if len(lines) > 1 {
				p := Presentation{Kind: "value", Primary: strings.TrimPrefix(lines[1], "HTTP "), Label: "Stale"}
				for _, line := range lines[2:] {
					if strings.HasPrefix(line, "Retry ") {
						p.Detail = line
					}
				}
				return p
			}
			label = "Stale"
		}
		if strings.HasPrefix(text, "Collecting") {
			label = "Connecting…"
		}
		return Presentation{Kind: "command", Label: label}
	}
	p := authoring.ConciseStatus(text)
	lines := strings.Split(text, "\n")
	label := strings.ReplaceAll(action, "-", " ")
	if len(label) > 0 {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	if text == "" {
		return Presentation{Kind: "command", Label: label}
	}
	swap := func() {
		if len(lines) > 1 {
			p.Primary, p.Label = lines[1], lines[0]
		}
	}
	switch slug {
	case "counter-tally", "randomizer-dice":
		p.Label = label
	case "microphone-controls":
		swap()
		p.Kind, p.Detail = "toggle", ""
	case "make", "tailscale", "retroachievements":
		swap()
	case "vlc":
		swap()
		p.Kind = "media"
		p.Label = strings.TrimPrefix(p.Label, "VLC · ")
		if action == "volume" || action == "volume-display" {
			p.Kind, p.Label = "value", "Volume"
		}
		if action == "repeat" || action == "random" {
			p.Kind, p.Label = "toggle", label
		}
		// File paths are useful in the inspector; keep them off the device
		if strings.ContainsAny(p.Primary, "/\\") && action != "progress" && action != "seek" {
			p.Primary = "Playing"
		}
	case "system-monitor", "battery-power":
		if before, after, found := strings.Cut(text, " "); found && strings.ContainsAny(after, "0123456789") {
			p.Primary, p.Label = after, before
		}
	case "http-webhooks":
		if action == "status" || action == "duration" {
			p.Primary, p.Label, p.Detail = strings.TrimPrefix(text, "HTTP "), label, ""
		} else {
			p = Presentation{Kind: "command", Label: label}
			if strings.Contains(text, "Working") {
				p.Label = "Working…"
			}
		}
	case "screenshot-tools":
		p = Presentation{Kind: "command", Label: label}
	case "service-monitor":
		p.Detail = "" // Last-check timestamps remain available in settings
	case "network-vpn":
		p.Detail = ""
	case "steam-library":
		if strings.ContainsAny(p.Primary, "/\\") {
			p.Primary = "Ready"
		}
		p.Detail = ""
	case "timers-pomodoro":
		swap()
		p.Kind = "progress"
	case "internet-speed-test":
		p.Primary = strings.TrimLeft(p.Primary, "↑↓ ")
	case "mqtt":
		// Preserve service values, exact MQTT fields, timer state, and dial values
	}
	// Long diagnostics and raw JSON remain available through logs/inspectors
	if len([]rune(p.Primary)) > 120 && strings.HasPrefix(strings.TrimSpace(p.Primary), "{") {
		p = Presentation{Kind: "command", Label: "Response received"}
	}
	if strings.TrimSpace(p.Label) == "" && p.Kind != "command" {
		p.Label = label
	}
	p.Label = strings.TrimSpace(p.Label)
	if strings.EqualFold(p.Primary, p.Label) {
		p.Label = ""
	}
	if strings.IndexFunc(p.Primary, unicode.IsControl) >= 0 {
		p.Primary = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, p.Primary)
	}
	return p
}

// TextStatus adapts ordinary text readers; semantic service/weather states stay
// with their plugins
func TextStatus(slug, action, text string, err error) DisplayStatus {
	if err != nil {
		return DisplayStatus{Text: "Unavailable\n" + err.Error(), Unavailable: true}
	}
	if text == slug && action != "" {
		text = strings.ToUpper(action[:1]) + action[1:]
	}
	return DisplayStatus{Text: text}
}

// AsyncStatus builds display feedback for asynchronous work
func AsyncStatus(text string) DisplayStatus {
	if text == "" {
		text = "Ready"
	}
	return DisplayStatus{Text: text, Unavailable: strings.HasPrefix(text, "Unavailable") || strings.HasPrefix(text, "Stale")}
}
