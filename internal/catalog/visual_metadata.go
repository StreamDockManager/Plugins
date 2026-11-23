package catalog

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	batterypower "github.com/StreamDockManager/Plugins/internal/plugins/battery-power"
	countertally "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
	homeassistant "github.com/StreamDockManager/Plugins/internal/plugins/home-assistant"
	httpwebhooks "github.com/StreamDockManager/Plugins/internal/plugins/http-webhooks"
	internetspeedtest "github.com/StreamDockManager/Plugins/internal/plugins/internet-speed-test"
	make "github.com/StreamDockManager/Plugins/internal/plugins/make"
	microphonecontrols "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
	mqtt "github.com/StreamDockManager/Plugins/internal/plugins/mqtt"
	networkvpn "github.com/StreamDockManager/Plugins/internal/plugins/network-vpn"
	obsstudio "github.com/StreamDockManager/Plugins/internal/plugins/obs-studio"
	osrshiscores "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	randommeme "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
	randomizerdice "github.com/StreamDockManager/Plugins/internal/plugins/randomizer-dice"
	retroachievements "github.com/StreamDockManager/Plugins/internal/plugins/retroachievements"
	runescapehiscores "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
	screenshottools "github.com/StreamDockManager/Plugins/internal/plugins/screenshot-tools"
	example "github.com/StreamDockManager/Plugins/internal/plugins/sdm-vault-example"
	servicemonitor "github.com/StreamDockManager/Plugins/internal/plugins/service-monitor"
	spotify "github.com/StreamDockManager/Plugins/internal/plugins/spotify"
	steamlibrary "github.com/StreamDockManager/Plugins/internal/plugins/steam-library"
	systemmonitor "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
	tailscale "github.com/StreamDockManager/Plugins/internal/plugins/tailscale"
	timerspomodoro "github.com/StreamDockManager/Plugins/internal/plugins/timers-pomodoro"
	vlc "github.com/StreamDockManager/Plugins/internal/plugins/vlc"
	weather "github.com/StreamDockManager/Plugins/internal/plugins/weather"
)

var actionVisuals = map[string]func(string) engine.Visual{
	"home-assistant":      homeassistant.VisualFor,
	"make":                make.VisualFor,
	"mqtt":                mqtt.VisualFor,
	"obs-studio":          obsstudio.VisualFor,
	"osrs-hiscores":       osrshiscores.VisualFor,
	"random-meme":         randommeme.VisualFor,
	"retroachievements":   retroachievements.VisualFor,
	"runescape-hiscores":  runescapehiscores.VisualFor,
	"tailscale":           tailscale.VisualFor,
	"vlc":                 vlc.VisualFor,
	"battery-power":       batterypower.VisualFor,
	"counter-tally":       countertally.VisualFor,
	"http-webhooks":       httpwebhooks.VisualFor,
	"internet-speed-test": internetspeedtest.VisualFor,
	"microphone-controls": microphonecontrols.VisualFor,
	"network-vpn":         networkvpn.VisualFor,
	"randomizer-dice":     randomizerdice.VisualFor,
	"screenshot-tools":    screenshottools.VisualFor,
	"service-monitor":     servicemonitor.VisualFor,
	"steam-library":       steamlibrary.VisualFor,
	"system-monitor":      systemmonitor.VisualFor,
	"timers-pomodoro":     timerspomodoro.VisualFor,
	"spotify":             spotify.VisualFor,
	"weather":             weather.VisualFor,
	example.Slug:          example.VisualFor,
}
