package catalog

import "maps"

// init registers the bundled catalog artwork
func init() {
	maps.Copy(packageGlyphs, map[string]string{
		"obs-studio":         `<rect x="2" y="3" width="20" height="16" rx="2"/><circle cx="12" cy="11" r="4"/><path d="M8 23h8M12 19v4"/>`,
		"home-assistant":     `<path d="m2 11 10-9 10 9M5 9v13h14V9M10 22v-8h4v8"/>`,
		"vlc":                `<path d="m12 2 7 18H5zM8 10h8M6 16h12M2 22h20"/>`,
		"tailscale":          `<circle cx="5" cy="5" r="2"/><circle cx="12" cy="5" r="2"/><circle cx="19" cy="5" r="2"/><circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/><circle cx="5" cy="19" r="2"/><circle cx="12" cy="19" r="2"/><circle cx="19" cy="19" r="2"/>`,
		"mqtt":               `<path d="M3 3a18 18 0 0 1 18 18M3 9a12 12 0 0 1 12 12M3 15a6 6 0 0 1 6 6"/><circle cx="3" cy="21" r="1"/>`,
		"make":               `<path d="M2 5h5v14H2zM10 5h5v14h-5zM18 5h4v14h-4zM7 9h3M15 15h3"/>`,
		"osrs-hiscores":      `<path d="m3 2 7 4 8 13-3 2L7 8zM21 2l-7 4-8 13 3 2 8-13zM2 17l6 4M16 21l6-4"/>`,
		"runescape-hiscores": `<path d="m12 1 8 7-3 13H7L4 8zM10 6v12M10 6l6 3-6 4M10 13l5 5"/>`,
		"retroachievements":  `<path d="M7 2h10v8a5 5 0 0 1-10 0zM7 5H2v4a5 5 0 0 0 5 5M17 5h5v4a5 5 0 0 1-5 5M12 15v5M7 22h10M9 20h6"/>`,
	})
}
