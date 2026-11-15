package catalog

import "maps"

// Each package has original geometry. These are intentionally independent of
// the first action so catalogue artwork remains stable as actions are added
var packageGlyphs = map[string]string{
	"random-meme":         `<rect x="2" y="2" width="20" height="20" rx="4"/><path d="M6 8h4M14 8h4M6 14q6 8 12 0M4 4l2 2M18 18l2 2"/>`,
	"counter-tally":       `<rect x="2" y="5" width="20" height="14" rx="3"/><path d="M7 9v6M4 12h6M14 9h5M14 15h5"/>`,
	"timers-pomodoro":     `<path d="M8 3h8M12 3v3"/><circle cx="12" cy="14" r="8"/><path d="M12 9v5l4 2M2 3l3 3M22 3l-3 3"/>`,
	"system-monitor":      `<rect x="2" y="3" width="20" height="16" rx="2"/><path d="M4 13h4l2-6 4 10 2-4h4M8 22h8M12 19v3"/>`,
	"screenshot-tools":    `<path d="M2 7h5l2-4h6l2 4h5v14H2z"/><circle cx="12" cy="13" r="4"/><path d="M3 2v2M1 3h4"/>`,
	"weather":             `<circle cx="7" cy="7" r="4"/><path d="M7 1v1M1 7h1M3 3l1 1M11 11a5 5 0 0 1 9 3 4 4 0 0 1 0 8H9a4 4 0 0 1 2-8"/>`,
	"randomizer-dice":     `<path d="m12 1 10 6v10l-10 6-10-6V7zM2 7l10 6 10-6M12 13v10M8 7h.1M16 17h.1M6 16h.1"/>`,
	"battery-power":       `<path d="M7 2h10v3h3v17H4V5h3zM13 7l-5 7h5l-2 5 6-8h-5z"/>`,
	"network-vpn":         `<path d="m12 2 9 4v6c0 5-5 8-9 10-4-2-9-5-9-10V6zM7 10a7 7 0 0 1 10 0M9 13a4 4 0 0 1 6 0M12 16h.1"/>`,
	"spotify":             `<circle cx="12" cy="12" r="10"/><path d="M5 9q7-4 14 0M6 13q6-3 12 0M8 17q4-2 8 0"/>`,
	"microphone-controls": `<rect x="8" y="2" width="8" height="12" rx="4"/><path d="M4 10v2a8 8 0 0 0 16 0v-2M12 20v3M8 23h8M8 7h8"/>`,
	"http-webhooks":       `<path d="M5 4h14v5H5zM5 15h14v5H5zM8 9v6M16 9v6M1 6h4M19 18h4M20 15l3 3-3 3"/>`,
	"service-monitor":     `<path d="M3 2h18v6H3zM3 10h18v6H3zM3 18h11M6 5h.1M6 13h.1M15 21l2 2 6-7"/>`,
	"internet-speed-test": `<path d="M2 20a10 10 0 1 1 20 0M12 17l7-9M3 10l3 2M7 4l1 3M15 4l-1 3M19 10l-2 2"/><circle cx="12" cy="17" r="2"/>`,
	"steam-library":       `<path d="M2 2h5v20H2zM9 4h5v18H9zM16 3l5-1 3 19-5 1zM3 7h3M10 8h3M18 7l4-1"/>`,
}

// init registers the bundled catalog artwork
func init() {
	extra := map[string]string{
		"pause":    `<path d="M6 3h4v18H6zM14 3h4v18h-4z"/>`,
		"record":   `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5" fill="currentColor"/>`,
		"input":    `<rect x="8" y="3" width="14" height="18" rx="2"/><path d="M1 12h14M11 8l4 4-4 4"/>`,
		"request":  `<path d="M2 5h15l-3-3M17 5l-3 3M22 19H7l3-3M7 19l3 3M3 9v6M21 9v6"/>`,
		"latency":  `<circle cx="12" cy="12" r="9"/><path d="M12 5v7l5 3M2 2l3 3"/>`,
		"download": `<path d="M12 2v14M6 10l6 6 6-6M3 16v6h18v-6"/>`,
		"upload":   `<path d="M12 16V2M6 8l6-6 6 6M3 16v6h18v-6"/>`,
		"cancel":   `<circle cx="12" cy="12" r="10"/><path d="m7 7 10 10M17 7 7 17"/>`,
		"folder":   `<path d="M2 5h8l2 3h10v13H2zM2 8h20"/>`,
		"bookmark": `<path d="M5 2h14v20l-7-5-7 5z"/>`,
		"next":     `<path d="m3 3 14 9L3 21zM21 3v18"/>`,
		"previous": `<path d="m21 3-14 9 14 9zM3 3v18"/>`,
		"deafen":   `<path d="M3 14v-3a9 9 0 0 1 18 0v3M3 13h4v9H3zM17 13h4v9h-4zM2 2l20 20"/>`,
		"held":     `<rect x="3" y="3" width="18" height="18" rx="3"/><path d="M7 11l5 5 5-5M12 6v10"/>`,
		"busy":     `<path d="M5 2h14M5 22h14M7 2v5l10 10v5M17 2v5L7 17v5M9 18h6"/>`,
	}
	maps.Copy(glyphs, extra)
}
