package hiscores

import (
	"context"
	"strings"

	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// Factory leaves scheduling and lifecycle with the shared connector; only
// Hiscores' presentation is specialized, as for the existing Spotify displays
func Factory(game Game, options c.Options) engine.Factory {
	base := c.Factory(options)
	return func(e *engine.Engine) engine.Feature {
		feature := base(e)
		status := feature.Status
		feature.Status = func(ctx context.Context, id string, b engine.Binding) engine.DisplayStatus {
			value := status(ctx, id, b)
			if value.Unavailable {
				lines := strings.Split(value.Text, "\n")
				if len(lines) > 1 {
					value.Text = lines[1]
				}
				return value
			}
			if value.Text == "Waiting for connection" {
				value.Text = "Loading"
			}
			settings := e.MergedSettings(b.Settings)
			skillID := c.String(settings, "skill", "0")
			action := engine.Action(b.Action)
			if action == "overall" || action == "refresh" {
				skillID = "0"
			}
			if skill, ok := game.Skill(skillID); ok && action != "activity" {
				value.Icon = skill.Cape
				metric := c.String(settings, "metric", "level")
				if action == "xp-change" || action == "reset-baseline" || action == "skill" && (metric == "xp" || metric == "rank") {
					value.Icon = skill.Icon
				}
			}
			return value
		}
		feature.View = func(id string, status engine.DisplayStatus) engine.Display {
			text := status.Text
			stale := strings.HasPrefix(text, "Stale\n")
			text = strings.TrimPrefix(text, "Stale\n")
			w, h := 112, 112
			if surface := e.Bindings[id].Surface; surface != nil && surface.Width > 0 && surface.Height > 0 {
				w, h = surface.Width, surface.Height
			}
			lines := strings.SplitN(text, "\n", 3)
			if status.Unavailable || len(lines) < 2 || status.Icon == "" || engine.Action(e.Bindings[id].Action) == "activity" {
				view := e.BuildDisplayView(id, status)
				if !status.Unavailable && strings.HasPrefix(status.Icon, "hiscores/") {
					view.Image = status.Icon + ".png"
					if icon, ok := view.Feedback["icon"].(map[string]any); ok {
						icon["value"] = view.Image
						if rect, ok := icon["rect"].([4]int); ok {
							icon["rect"] = game.FitArtwork(status.Icon, rect, w, h)
						}
					}
				}
				return view
			}
			shape := "key"
			if w > h*13/10 {
				shape = "wide"
			}
			detail := ""
			if len(lines) == 3 {
				detail = lines[2]
			}
			variant := "level"
			if detail != "" {
				variant = "detail"
			}
			if len([]rune(lines[1])) > 13 {
				variant = "number"
			}
			name := lines[0]
			stateLabel := ""
			if stale {
				stateLabel = "Stale"
			}
			image := status.Icon + ".png"
			layout := "hiscores/" + variant + "-" + shape + ".json"
			if e.Theme != "" {
				layout = strings.TrimSuffix(layout, ".json") + "-" + e.Theme + ".json"
			}
			return engine.Display{Layout: layout, Image: image, Title: status.Text, Feedback: map[string]any{
				"cape": map[string]any{"value": image, "rect": game.FitArtwork(status.Icon, ArtworkRect(variant, shape == "wide"), w, h)},
				"name": name, "value": lines[1], "detail": detail, "status": stateLabel,
			}}
		}
		return feature
	}
}
