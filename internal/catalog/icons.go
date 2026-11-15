package catalog

import (
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
	"github.com/StreamDockManager/Plugins/internal/engine"
	randommeme "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
	"maps"
	"strings"
)

// ActionVisual is shared by the manifest and runtime. State images must use the
// same ordering on keys and touch displays
type ActionVisual = engine.Visual

// VisualFor resolves the declared visual for an action
func VisualFor(slug, action string) ActionVisual {
	if lookup := actionVisuals[slug]; lookup != nil {
		return lookup(action)
	}
	return ActionVisual{}
}

// Original, code-native SVG glyphs. A square viewBox and generous padding keep
// the glyphs legible when the host scales them onto different surface shapes
var glyphs = map[string]string{
	"video":             `<rect x="2" y="4" width="15" height="16" rx="2"/><path d="m17 9 5-3v12l-5-3"/>`,
	"skill-cape":        `<path d="M8 3 4 7l2 14 6 2 6-2 2-14-4-4-4 2zM8 3l4 2 4-2M8 8v11M16 8v11"/><path d="m12 9 3 3-3 4-3-4z"/>`,
	"plus":              `<path d="M12 4v16M4 12h16"/>`,
	"minus":             `<path d="M4 12h16"/>`,
	"plus-minus":        `<path d="M7 3v8M3 7h8M13 17h8"/>`,
	"reset":             `<path d="M4 8a9 9 0 1 1-1 7M4 3v5h5"/>`,
	"timer":             `<circle cx="12" cy="14" r="8"/><path d="M9 2h6M12 2v4M12 14V9M18 7l2-2"/>`,
	"stopwatch":         `<circle cx="12" cy="14" r="8"/><path d="M9 2h6M12 2v4M12 9v5l3 2M18 7l2-2"/>`,
	"tomato":            `<path d="M12 7c-11-6-15 13-3 14h6C27 20 23 1 12 7ZM8 2l4 5 4-5M12 7l-5 3M12 7l5 3"/>`,
	"timer-adjust":      `<path d="M9 2h6M12 2v4M12 14V9M13 22a8 8 0 1 1 7-11M19 14v8M15 18h8"/>`,
	"timer-pause":       `<path d="M9 2h6M12 2v4"/><circle cx="12" cy="14" r="8"/><path d="M10 10v8M14 10v8"/>`,
	"check":             `<circle cx="12" cy="12" r="9"/><path d="m7 12 3 3 7-7"/>`,
	"coffee":            `<path d="M3 8h14v8a5 5 0 0 1-5 5H8a5 5 0 0 1-5-5ZM17 8h2a3 3 0 0 1 0 6h-2M6 2v3M10 1v4M14 2v3"/>`,
	"cpu":               `<rect x="5" y="5" width="14" height="14" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M8 1v4M16 1v4M8 19v4M16 19v4M1 8h4M1 16h4M19 8h4M19 16h4"/>`,
	"memory":            `<path d="M2 6h20v12H2ZM6 18v3M10 18v3M14 18v3M18 18v3M6 10v4M10 10v4M14 10v4M18 10v4"/>`,
	"disk":              `<path d="m5 3-3 12v6h20v-6L19 3ZM2 15h20M6 18h1M17 18h1"/>`,
	"network":           `<rect x="8" y="2" width="8" height="6" rx="1"/><path d="M12 8v6M4 14h16M4 14v3M12 14v3M20 14v3"/><path d="M1 17h6v5H1zM9 17h6v5H9zM17 17h6v5h-6z"/>`,
	"speaker":           `<path d="M11 4 6 8H2v8h4l5 4zM15 8a6 6 0 0 1 0 8M18 4a11 11 0 0 1 0 16"/>`,
	"speaker-off":       `<path d="M11 4 6 8H2v8h4l5 4zM16 9l6 6M22 9l-6 6"/>`,
	"speaker-toggle":    `<path d="M10 5 6 8H2v8h4l4 3ZM15 6h7l-3-3M22 18h-7l3 3M15 9V6M22 15v3"/>`,
	"headphones":        `<path d="M3 14v-3a9 9 0 0 1 18 0v3M3 13h4v9H3zM17 13h4v9h-4z"/>`,
	"app-speaker":       `<rect x="2" y="3" width="20" height="18" rx="2"/><path d="M2 8h20M13 10l-4 3H6v4h3l4 3ZM17 12a4 4 0 0 1 0 6"/>`,
	"microphone":        `<rect x="8" y="2" width="8" height="13" rx="4"/><path d="M5 10v2a7 7 0 0 0 14 0v-2M12 19v3M8 22h8"/>`,
	"microphone-off":    `<path d="M8 6V5a4 4 0 0 1 8 0v7M8 10v2a4 4 0 0 0 6 3M5 10v2a7 7 0 0 0 12 5M19 10v2M12 19v3M8 22h8M2 2l20 20"/>`,
	"microphone-toggle": `<rect x="5" y="2" width="7" height="12" rx="3.5"/><path d="M2 10v2a6.5 6.5 0 0 0 13 0M8 19v3M4 22h8M17 4h5l-2-2M22 9h-5l2 2"/>`,
	"play":              `<path d="m6 3 15 9L6 21z" fill="currentColor" stroke="none"/>`,
	"stop":              `<rect x="4" y="4" width="16" height="16" rx="2" fill="currentColor" stroke="none"/>`,
	"play-stop":         `<path d="m2 4 10 8L2 20z" fill="currentColor" stroke="none"/><rect x="15" y="5" width="7" height="14"/>`,
	"sparkles":          `<path d="m12 2 3 7 7 3-7 3-3 7-3-7-7-3 7-3zM3 2v4M1 4h4"/>`,
	"sparkles-on":       `<path d="m12 2 3 7 7 3-7 3-3 7-3-7-7-3 7-3z" fill="currentColor"/><path d="M3 2v4M1 4h4"/>`,
	"tile":              `<rect x="2" y="3" width="20" height="18" rx="1"/><path d="M12 3v18M12 12h10"/>`,
	"maximize":          `<path d="M3 9V3h6M15 3h6v6M21 15v6h-6M9 21H3v-6"/>`,
	"resize":            `<path d="M4 14V4h10M4 4l6 6M20 10v10H10M20 20l-6-6"/>`,
	"move":              `<path d="M12 2v20M2 12h20M8 6l4-4 4 4M8 18l4 4 4-4M6 8l-4 4 4 4M18 8l4 4-4 4"/>`,
	"save":              `<path d="M3 2h15l4 4v16H3ZM7 2v7h10V2M7 22v-9h11v9"/>`,
	"restore":           `<path d="M4 8a9 9 0 1 1-1 7M4 3v5h5M12 7v6l4 2"/>`,
	"display":           `<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M12 17v4M7 21h10"/>`,
	"window":            `<rect x="2" y="3" width="20" height="18" rx="2"/><path d="M2 8h20M5 5.5h1M9 5.5h1"/>`,
	"crop":              `<path d="M6 2v16h16M2 6h16v16M10 2h3M2 10v3M22 11v3M11 22h3"/>`,
	"clipboard":         `<path d="M8 4H4v18h16V4h-4M8 2h8v5H8zM8 12h8M8 16h6"/>`,
	"cloud-sun":         `<path d="M6 15a4 4 0 1 1 8-2 4 4 0 1 1 4 8H6a3 3 0 0 1 0-6"/><path d="M12 6a4 4 0 1 1 6 5M15 1v2M21 3l-2 2M22 9h-2"/>`,
	"forecast":          `<rect x="2" y="4" width="20" height="18" rx="2"/><path d="M2 9h20M7 2v4M17 2v4M7 15h2M15 17h2"/><circle cx="12" cy="15" r="3"/>`,
	"sun":               `<circle cx="12" cy="12" r="4"/><path d="M12 1v3M12 20v3M1 12h3M20 12h3M4 4l2 2M18 18l2 2M4 20l2-2M18 6l2-2"/>`,
	"eyedropper":        `<path d="m14 4 6 6M12 6l6 6M14 8 4 18l-1 4 4-1L17 11M14 4l2-2a3 3 0 0 1 4 4l-2 2"/>`,
	"copy":              `<rect x="8" y="8" width="13" height="14" rx="2"/><path d="M16 8V2H3v14h5"/>`,
	"palette":           `<path d="M12 2a10 10 0 1 0 0 20h2a3 3 0 0 0 1-6c-2-1-1-3 1-3h3c5 0 3-11-7-11Z"/><circle cx="7" cy="8" r="1"/><circle cx="12" cy="6" r="1"/><circle cx="17" cy="8" r="1"/><circle cx="5" cy="13" r="1"/>`,
	"cycle":             `<path d="M3 9a9 9 0 0 1 16-3l2 3M21 3v6h-6M21 15A9 9 0 0 1 5 18l-2-3M3 21v-6h6"/>`,
	"dice":              `<rect x="3" y="3" width="18" height="18" rx="3"/><path d="M7 7h.1M17 7h.1M12 12h.1M7 17h.1M17 17h.1" stroke-width="3"/>`,
	"range":             `<path d="M3 6h18M3 18h18M7 3v6M17 15v6M3 12h18M12 9v6"/>`,
	"list":              `<path d="M9 5h13M9 12h13M9 19h13M2 5h1M2 12h1M2 19h1"/>`,
	"cards":             `<path d="m4 9-3 1 4 12 8-3M9 5 6 6l3 14 9-2"/><rect x="12" y="2" width="10" height="15" rx="1"/><path d="m17 7 2 3-2 3-2-3z"/>`,
	"battery":           `<rect x="2" y="6" width="18" height="12" rx="2"/><path d="M22 10v4M6 9v6M10 9v6M14 9v6"/>`,
	"globe":             `<circle cx="12" cy="12" r="10"/><ellipse cx="12" cy="12" rx="4" ry="10"/><path d="M2 12h20"/>`,
	"shield":            `<path d="m12 2 9 4v6c0 5-5 9-9 11-4-2-9-6-9-11V6ZM8 12l3 3 5-6"/>`,
	"calculator":        `<rect x="4" y="2" width="16" height="20" rx="2"/><path d="M7 5h10v4H7zM8 13h1M15 13h1M8 17h1M15 17h1"/>`,
	"convert":           `<path d="M3 7h18l-5-5M21 17H3l5 5M21 7l-5 5M3 17l5-5"/>`,
	"unavailable":       `<path d="m12 2 11 20H1zM12 9v5M12 18h.1"/>`,
}

// iconSVG builds the SVG artwork for the selected icon
func iconSVG(path string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g transform="translate(4 4)" fill="none" stroke="#f5f7fa" color="#f5f7fa" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + path + `</g></svg>`)
}

var displayLayout = authoring.DisplayLayout

// PluginAssets collects the assets required by one plugin
func PluginAssets(d Definition) map[string][]byte {
	files := map[string][]byte{"display/key.json": displayLayout(false), "display/wide.json": displayLayout(true)}
	if d.Slug == "random-meme" {
		files["display/meme.json"], _ = json.Marshal(randommeme.Layout())
		files[randommeme.CaptionAsset] = []byte(randommeme.CaptionSVG)
	}
	if d.Slug == "weather" {
		for _, action := range d.Actions {
			for _, wide := range []bool{false, true} {
				shape := "key"
				if wide {
					shape = "wide"
				}
				files[fmt.Sprintf("display/weather-%s-%s.json", action, shape)] = weatherLayout(action, wide)
			}
		}
	}
	if d.Slug == "spotify" {
		maps.Copy(files, SpotifyAssets())
	}
	if d.Slug == "osrs-hiscores" || d.Slug == "runescape-hiscores" {
		maps.Copy(files, HiscoresAssets(d.Slug))
	}
	for _, asset := range RequiredAssets(d) {
		name := strings.TrimPrefix(asset, "icons/")
		if path, ok := glyphs[name]; ok {
			files[asset+".svg"] = iconSVG(path)
		}
	}
	// Keep specialized fields/artwork and their saved identities while using
	// the same measured bundled-font path as the semantic presentations
	if d.Slug == "weather" || d.Slug == "spotify" || d.Slug == "osrs-hiscores" || d.Slug == "runescape-hiscores" {
		for name, data := range files {
			if !strings.HasPrefix(name, "display/") || !strings.HasSuffix(name, ".json") {
				continue
			}
			var layout map[string]any
			if json.Unmarshal(data, &layout) != nil {
				continue
			}
			items, _ := layout["items"].([]any)
			for _, value := range items {
				item, ok := value.(map[string]any)
				if !ok || item["type"] != "text" {
					continue
				}
				font, ok := item["font"].(map[string]any)
				if !ok {
					font = map[string]any{}
					item["font"] = font
				}
				font["fit"] = true
			}
			files[name], _ = json.Marshal(layout)
		}
	}
	files["icon.svg"] = iconSVG(packageGlyphs[d.Slug])
	paths := []string{}
	for path := range files {
		if strings.HasSuffix(path, ".svg") {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		data := files[path]
		if path != "icon.svg" {
			data = markIconRoles(data)
			files[path] = data
		}
		for _, theme := range []string{"light", "dark"} {
			p := palettes[theme]
			svg := string(data)
			for _, old := range []string{"#0f141d", "#080808"} {
				svg = strings.ReplaceAll(svg, old, p["surface"])
			}
			for _, old := range []string{"#f5f7fa", "#fafafa"} {
				svg = strings.ReplaceAll(svg, old, p["ink"])
			}
			files[ThemeAsset(path, theme)] = []byte(svg)
		}
	}
	layouts := []string{}
	for path := range files {
		if strings.HasSuffix(path, ".json") {
			layouts = append(layouts, path)
		}
	}
	for _, path := range layouts {
		files[path] = markLayoutRoles(files[path])
		for _, theme := range []string{"light", "dark"} {
			var layout map[string]any
			_ = json.Unmarshal(files[path], &layout)
			palette := palettes[theme]
			layout["background"] = palette["surface"]
			if items, ok := layout["items"].([]any); ok {
				for _, value := range items {
					item := value.(map[string]any)
					item["color"] = palette["ink"]
					item["bar_fill_c"] = palette["accent"]
					item["bar_bg_c"] = palette["border"]
				}
			}
			data, _ := json.Marshal(layout)
			files[strings.TrimSuffix(path, ".json")+"-"+theme+".json"] = data
		}
	}

	return files
}

// weatherLayout builds the weather display layout
func weatherLayout(action string, wide bool) []byte {
	icon := [4]int{55, 0, 90, 43}
	value := [4]int{4, 46, 192, 48}
	if wide {
		icon = [4]int{2, 4, 72, 88}
		value = [4]int{78, 8, 120, 84}
	}
	items := []map[string]any{{"key": "icon", "label": "Weather icon", "type": "pixmap", "rect": icon}}
	keys, labels := []string{"temperature"}, []string{"Temperature"}
	if action == "forecast" {
		keys, labels = []string{"low", "high"}, []string{"Low temperature", "High temperature"}
	}
	if action == "sun" {
		keys, labels = []string{"sunrise", "sunset"}, []string{"Sunrise", "Sunset"}
	}
	for i, key := range keys {
		rect := value
		rect[1] += i * value[3] / len(keys)
		rect[3] /= len(keys)
		items = append(items, map[string]any{"key": key, "label": labels[i], "type": "text", "rect": rect, "font": map[string]any{"size": min(30, rect[3])}})
	}
	data, _ := json.Marshal(map[string]any{"id": "weather-" + action + "-" + map[bool]string{true: "wide", false: "key"}[wide] + "-v1", "items": items})
	return data
}
