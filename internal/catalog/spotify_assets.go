package catalog

import "encoding/json"

// SpotifyAssets is shared by packaging and the live displays. Paths stay inside
// the package, so icons and feedback layouts also work offline
func SpotifyAssets() map[string][]byte {
	paths := map[string]string{
		"play":     `<path d="m8 4 12 8-12 8z" fill="currentColor" stroke="none"/>`,
		"pause":    `<path d="M7 4h3v16H7zM14 4h3v16h-3z" fill="currentColor" stroke="none"/>`,
		"toggle":   `<path d="m2 5 9 7-9 7z" fill="currentColor" stroke="none"/><path d="M15 5v14M21 5v14"/>`,
		"next":     `<path d="m5 4 12 8L5 20z" fill="currentColor" stroke="none"/><path d="M20 4v16"/>`,
		"previous": `<path d="m19 4-12 8 12 8z" fill="currentColor" stroke="none"/><path d="M4 4v16"/>`,
		"volume":   `<path d="M11 4 6 8H2v8h4l5 4zM15 8a6 6 0 0 1 0 8M18 4a11 11 0 0 1 0 16"/>`,
		"seek":     `<path d="M3 12h18M16 7l5 5-5 5M8 5v14"/>`,
		"status":   `<path d="M9 18V5l12-2v13M9 9l12-2"/><ellipse cx="6" cy="18" rx="3" ry="2"/><ellipse cx="18" cy="16" rx="3" ry="2"/>`,
	}
	files := map[string][]byte{}
	paths["volume-display"] = paths["volume"]
	paths["mute-toggle"] = `<path d="M11 4 6 8H2v8h4l5 4zM16 9l6 6M22 9l-6 6"/>`
	paths["progress"] = paths["seek"]
	paths["track-info"] = paths["status"]
	for name, path := range paths {
		files["spotify/"+name+".svg"] = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g transform="translate(4 4)" fill="none" stroke="#f5f7fa" color="#f5f7fa" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + path + `</g></svg>`)
	}
	pixmap := func(key string, rect [4]int) map[string]any {
		return map[string]any{"key": key, "type": "pixmap", "rect": rect}
	}
	text := func(key string, rect [4]int) map[string]any {
		return map[string]any{"key": key, "type": "text", "rect": rect, "font": map[string]any{"size": 10, "fit": true}}
	}
	layouts := map[string][]map[string]any{
		"artwork":        {pixmap("art", [4]int{0, 0, 200, 100})},
		"volume-display": {pixmap("icon", [4]int{72, 0, 56, 28}), text("label", [4]int{8, 62, 184, 22}), text("value", [4]int{8, 30, 184, 32}), {"key": "indicator", "type": "bar", "rect": [4]int{8, 88, 184, 6}, "range": map[string]int{"min": 0, "max": 100}}},
		"progress":       {pixmap("icon", [4]int{72, 0, 56, 28}), text("label", [4]int{8, 62, 184, 22}), text("time", [4]int{8, 30, 184, 32}), {"key": "indicator", "type": "bar", "rect": [4]int{8, 88, 184, 6}, "range": map[string]int{"min": 0, "max": 100}}},
		"track-info":     {pixmap("art", [4]int{4, 4, 52, 52}), text("title", [4]int{4, 60, 192, 18}), text("artist", [4]int{4, 82, 192, 16})},
		"control":        {pixmap("icon", [4]int{38, 0, 124, 62}), text("label", [4]int{8, 62, 184, 32})},
		"status-key": {
			pixmap("art", [4]int{40, 0, 120, 60}),
			text("mode", [4]int{0, 62, 200, 14}),
			text("time", [4]int{0, 80, 200, 18}),
			text("position", [4]int{0, 76, 200, 10}),
			text("duration", [4]int{0, 88, 200, 10}),
		},
		"status-wide": {
			pixmap("art", [4]int{4, 4, 88, 70}),
			text("mode", [4]int{96, 20, 100, 24}),
			text("time", [4]int{0, 80, 200, 18}),
			text("position", [4]int{0, 76, 200, 10}),
			text("duration", [4]int{0, 88, 200, 10}),
		},
	}
	for _, layout := range layouts {
		for _, item := range layout {
			if item["type"] == "text" {
				item["font"].(map[string]any)["size"] = min(30, item["rect"].([4]int)[3])
			}
		}
	}
	for name, items := range layouts {
		data, _ := json.MarshalIndent(map[string]any{"id": "spotify-" + name, "items": items}, "", "    ")
		files["spotify/"+name+".json"] = append(data, '\n')
	}
	return files
}
