// modernreview emits deterministic desired views for the native compositor
// It never runs a plugin action, polls a service, or opens hardware
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/catalog"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	media "github.com/StreamDockManager/Plugins/internal/native/spotify"
	p1 "github.com/StreamDockManager/Plugins/internal/plugins/battery-power"
	p2 "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
	homeassistant "github.com/StreamDockManager/Plugins/internal/plugins/home-assistant"
	p5 "github.com/StreamDockManager/Plugins/internal/plugins/http-webhooks"
	p7 "github.com/StreamDockManager/Plugins/internal/plugins/internet-speed-test"
	p8 "github.com/StreamDockManager/Plugins/internal/plugins/make"
	p9 "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
	p11 "github.com/StreamDockManager/Plugins/internal/plugins/mqtt"
	p12 "github.com/StreamDockManager/Plugins/internal/plugins/network-vpn"
	obsstudio "github.com/StreamDockManager/Plugins/internal/plugins/obs-studio"
	p13 "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	randommeme "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
	p14 "github.com/StreamDockManager/Plugins/internal/plugins/randomizer-dice"
	p15 "github.com/StreamDockManager/Plugins/internal/plugins/retroachievements"
	p16 "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
	p18 "github.com/StreamDockManager/Plugins/internal/plugins/screenshot-tools"
	p19 "github.com/StreamDockManager/Plugins/internal/plugins/service-monitor"
	p20 "github.com/StreamDockManager/Plugins/internal/plugins/spotify"
	p21 "github.com/StreamDockManager/Plugins/internal/plugins/steam-library"
	p22 "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
	p23 "github.com/StreamDockManager/Plugins/internal/plugins/tailscale"
	p24 "github.com/StreamDockManager/Plugins/internal/plugins/timers-pomodoro"
	p25 "github.com/StreamDockManager/Plugins/internal/plugins/vlc"
	p27 "github.com/StreamDockManager/Plugins/internal/plugins/weather"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

var factories = map[string]engine.Factory{
	"obs-studio":     obsstudio.Factory,
	"home-assistant": homeassistant.Factory,

	"random-meme":         randommeme.Factory,
	"battery-power":       p1.Factory,
	"counter-tally":       p2.Factory,
	"http-webhooks":       p5.Factory,
	"internet-speed-test": p7.Factory,
	"make":                p8.Factory,
	"microphone-controls": p9.Factory,
	"mqtt":                p11.Factory,
	"network-vpn":         p12.Factory,
	"osrs-hiscores":       p13.Factory,
	"randomizer-dice":     p14.Factory,
	"retroachievements":   p15.Factory,
	"runescape-hiscores":  p16.Factory,
	"screenshot-tools":    p18.Factory,
	"service-monitor":     p19.Factory,
	"spotify":             p20.Factory,
	"steam-library":       p21.Factory,
	"system-monitor":      p22.Factory,
	"tailscale":           p23.Factory,
	"timers-pomodoro":     p24.Factory,
	"vlc":                 p25.Factory,
	"weather":             p27.Factory,
}

type reviewCase struct {
	name, text  string
	state       int
	unavailable bool
}

// main runs the modernreview command
func main() {
	sizes := flag.String("sizes", "64x64,72x72,80x80,85x85,96x96,100x100,112x112,176x112", "descriptor surface sizes")
	only := flag.String("plugin", "", "review one plugin slug")
	auditStates := flag.Bool("audit-states", false, "include every declared icon state and loading/stale/long values")
	lightPalette := flag.String("light-palette", "", "render the named light device palette")
	darkPalette := flag.String("dark-palette", "", "render the named dark device palette")
	flag.Parse()
	temp, err := os.MkdirTemp("", "modern-display-review-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(temp)
	output := json.NewEncoder(os.Stdout)
	for _, def := range catalog.Definitions {
		if *only != "" && def.Slug != *only {
			continue
		}
		e := fixtureengine.New(def, filepath.Join(temp, def.Slug), factories[def.Slug])
		assets := catalog.PluginAssets(def)
		for _, theme := range []string{"light", "dark"} {
			e.Theme = theme
			palette := *lightPalette
			if theme == "dark" {
				palette = *darkPalette
			}
			asset := func(path string) []byte {
				return catalog.PreviewPaletteAsset(path, theme, palette, assets[path])
			}
			for size := range strings.SplitSeq(*sizes, ",") {
				var width, height int
				if _, err := fmt.Sscanf(size, "%dx%d", &width, &height); err != nil {
					panic(err)
				}
				for _, action := range def.Actions {
					text := "Ready"
					switch def.Slug {
					case "counter-tally":
						text = "123456789012345"
					case "timers-pomodoro":
						text = "Focus\n12:34"
					case "system-monitor":
						text = "CPU 87.5%"
					case "microphone-controls":
						text = "Long named desk microphone\nMuted\nSystem input"
					case "weather":
						text = "19°C\n24°C"
					case "vlc":
						text = "Playing\nA song with a long title"
					case "http-webhooks":
						text = "200"
					case "internet-speed-test":
						text = "↓ 987.6 Mbps"
					case "randomizer-dice":
						text = "20"
					case "battery-power":
						text = "Battery 89%"
					}
					id := action + "-" + size
					e.Bindings[id] = engine.Binding{Action: catalog.ID(def.Slug) + "." + action, Settings: map[string]any{}, Surface: &sdk.Surface{Width: width, Height: height}}
					cases := []reviewCase{{name: "false"}, {name: "true", unavailable: true}}
					if def.Slug == "spotify" && action == "status" {
						cases = append(cases, reviewCase{name: "artwork-only"})
					}
					if *auditStates {
						for state := range catalog.VisualFor(def.Slug, action).States {
							cases = append(cases, reviewCase{name: fmt.Sprintf("state-%d", state), state: state})
						}
						for _, sample := range []struct{ name, text string }{
							{"loading", "Loading"},
							{"stale", "Stale\nLast successful sample\n123456789012345678901234567890"},
							{"long", "A long player or service name\n123456789012345678901234567890\nLong secondary detail"},
						} {
							cases = append(cases, reviewCase{name: sample.name, text: sample.text})
						}
					}
					for _, sample := range cases {
						unavailable := sample.unavailable
						status := engine.DisplayStatus{Text: text}
						if (def.Slug == "osrs-hiscores" || def.Slug == "runescape-hiscores") && action != "activity" {
							game := p13.Game
							if def.Slug == "runescape-hiscores" {
								game = p16.Game
							}
							skill := game.Skills[1]
							if action == "overall" || action == "refresh" {
								skill = game.Skills[0]
							}
							status.Text = fmt.Sprintf("%s\n80/%d", skill.Name, skill.Cap)
							status.Icon = skill.Cape
							if action == "xp-change" || action == "reset-baseline" {
								status.Text = skill.Name + "\n+12345\nFetched XP Δ"
								status.Icon = skill.Icon
							}
						}
						if strings.Contains(text, "Muted") {
							status.State = 1
						}
						if strings.HasPrefix(sample.name, "state-") {
							status.State = sample.state
						}
						if sample.text != "" {
							status.Text = sample.text
						}
						if unavailable {
							status.Text = "Unavailable\nDetailed diagnostic remains outside the device"
							status.Unavailable = true
						}
						view := e.DisplayView(id, status)
						if def.Slug == "random-meme" && !unavailable && sample.name != "loading" {
							width, height := 180, 100
							if sample.name == "long" {
								width, height = 100, 180
							}
							pixels := image.NewNRGBA(image.Rect(0, 0, width, height))
							for y := 0; y < height; y++ {
								for x := 0; x < width; x++ {
									pixels.Set(x, y, color.NRGBA{uint8(40 + x), uint8(50 + y), 160, 255})
								}
							}
							var pngData bytes.Buffer
							if err := png.Encode(&pngData, pixels); err != nil {
								panic(err)
							}
							meme := &randommeme.Meme{Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes()), Width: width, Height: height}
							caption := "Fetch again"
							if action == "send" {
								caption = "Send link"
							}
							view = randommeme.View(e.Bindings[id], meme, caption, theme)
						}
						if def.Slug == "spotify" {
							position, duration, volume := 45.0, 214.0, 100.0
							playback := media.SpotifyStatus{Mode: "playing", Title: "A song with a long title", Artist: "Artist", PositionSeconds: &position, DurationSeconds: &duration, VolumePercent: &volume}
							if unavailable {
								playback.Mode = "unavailable"
							}
							if sample.name == "long" {
								position, duration = 12345, 23456
							}
							if sample.name == "state-0" {
								playback.Mode = "paused"
							}
							view = p20.PlaybackView(e.Bindings[id], playback)
							if sample.name == "artwork-only" {
								view = p20.ArtworkView(e.Bindings[id])
							}
							if (action == "status" || action == "track-info") && !unavailable {
								// A deterministic album-cover fixture exercises the live raster path
								pixels := image.NewNRGBA(image.Rect(0, 0, 64, 64))
								for y := range 64 {
									for x := range 64 {
										pixels.Set(x, y, color.NRGBA{uint8(40 + x*2), uint8(50 + y*2), 160, 255})
									}
								}
								var cover bytes.Buffer
								if err := png.Encode(&cover, pixels); err != nil {
									panic(err)
								}
								view.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(cover.Bytes())
								if field, ok := view.Feedback["art"].(map[string]any); ok {
									field["value"] = view.Image
								} else {
									view.Feedback["art"] = view.Image
								}
							}
							view.Layout = e.ThemedLayout(view.Layout)
							for _, key := range []string{"icon", "art"} {
								if source, ok := view.Feedback[key].(string); ok {
									view.Feedback[key] = e.ThemedAsset(source)
								}
								if object, ok := view.Feedback[key].(map[string]any); ok {
									if source, ok := object["value"].(string); ok {
										object["value"] = e.ThemedAsset(source)
									}
								}
							}
						}
						var layout map[string]any
						if err := json.Unmarshal(asset(view.Layout), &layout); err != nil {
							panic(fmt.Sprintf("%s %s: %v", def.Slug, view.Layout, err))
						}
						if palette == "" {
							layout["background"] = "#00000000"
						}
						// Resolve the same packaged SVG/PNG assets used by Manager
						var resolve func(any) any
						resolve = func(value any) any {
							switch value := value.(type) {
							case string:
								if data := asset(value); len(data) > 0 {
									mime := "image/svg+xml"
									if strings.HasSuffix(value, ".png") {
										mime = "image/png"
									}
									return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
								}
							case map[string]any:
								for key, child := range value {
									value[key] = resolve(child)
								}
							}
							return value
						}
						feedback := resolve(view.Feedback)
						name := fmt.Sprintf("%s-%s-%s-%s-%s", def.Slug, action, theme, size, sample.name)
						if err := output.Encode(map[string]any{"name": name, "width": width, "height": height, "layout": layout, "feedback": feedback, "title": view.Title, "plugin": def.Slug, "action": action, "unavailable": unavailable}); err != nil {
							panic(err)
						}
					}
				}
			}
		}
		_ = e.Close()
	}
}
