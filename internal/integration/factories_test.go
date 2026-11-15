package integration

import (
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	homeassistant "github.com/StreamDockManager/Plugins/internal/plugins/home-assistant"
	make "github.com/StreamDockManager/Plugins/internal/plugins/make"
	mqtt "github.com/StreamDockManager/Plugins/internal/plugins/mqtt"
	obsstudio "github.com/StreamDockManager/Plugins/internal/plugins/obs-studio"
	osrshiscores "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	randommeme "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
	retroachievements "github.com/StreamDockManager/Plugins/internal/plugins/retroachievements"
	runescapehiscores "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
	tailscale "github.com/StreamDockManager/Plugins/internal/plugins/tailscale"
	vlc "github.com/StreamDockManager/Plugins/internal/plugins/vlc"

	"fmt"

	"github.com/StreamDockManager/Plugins/internal/catalog"
	"github.com/StreamDockManager/Plugins/internal/engine"
	batterypower "github.com/StreamDockManager/Plugins/internal/plugins/battery-power"
	countertally "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
	httpwebhooks "github.com/StreamDockManager/Plugins/internal/plugins/http-webhooks"
	internetspeedtest "github.com/StreamDockManager/Plugins/internal/plugins/internet-speed-test"
	microphonecontrols "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
	networkvpn "github.com/StreamDockManager/Plugins/internal/plugins/network-vpn"
	randomizerdice "github.com/StreamDockManager/Plugins/internal/plugins/randomizer-dice"
	screenshottools "github.com/StreamDockManager/Plugins/internal/plugins/screenshot-tools"
	servicemonitor "github.com/StreamDockManager/Plugins/internal/plugins/service-monitor"
	"github.com/StreamDockManager/Plugins/internal/plugins/spotify"
	steamlibrary "github.com/StreamDockManager/Plugins/internal/plugins/steam-library"
	systemmonitor "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
	timerspomodoro "github.com/StreamDockManager/Plugins/internal/plugins/timers-pomodoro"
	"github.com/StreamDockManager/Plugins/internal/plugins/weather"
)

var Definitions = catalog.Definitions

// New constructs the fixture or runtime instance with its dependencies
func New(slug, path string) *engine.Engine {
	switch slug {
	case "obs-studio":
		return fixtureengine.New(obsstudio.Definition, path, obsstudio.Factory)
	case "home-assistant":
		return fixtureengine.New(homeassistant.Definition, path, homeassistant.Factory)

	case "random-meme":
		return fixtureengine.New(randommeme.Definition, path, randommeme.Factory)
	case "vlc":
		return fixtureengine.New(vlc.Definition, path, vlc.Factory)
	case "tailscale":
		return fixtureengine.New(tailscale.Definition, path, tailscale.Factory)
	case "mqtt":
		return fixtureengine.New(mqtt.Definition, path, mqtt.Factory)
	case "make":
		return fixtureengine.New(make.Definition, path, make.Factory)
	case "osrs-hiscores":
		return fixtureengine.New(osrshiscores.Definition, path, osrshiscores.Factory)
	case "runescape-hiscores":
		return fixtureengine.New(runescapehiscores.Definition, path, runescapehiscores.Factory)
	case "retroachievements":
		return fixtureengine.New(retroachievements.Definition, path, retroachievements.Factory)

	case "battery-power":
		return fixtureengine.New(batterypower.Definition, path, batterypower.Factory)
	case "counter-tally":
		return fixtureengine.New(countertally.Definition, path, countertally.Factory)
	case "http-webhooks":
		return fixtureengine.New(httpwebhooks.Definition, path, httpwebhooks.Factory)
	case "internet-speed-test":
		return fixtureengine.New(internetspeedtest.Definition, path, internetspeedtest.Factory)
	case "microphone-controls":
		return fixtureengine.New(microphonecontrols.Definition, path, microphonecontrols.Factory)
	case "network-vpn":
		return fixtureengine.New(networkvpn.Definition, path, networkvpn.Factory)
	case "randomizer-dice":
		return fixtureengine.New(randomizerdice.Definition, path, randomizerdice.Factory)
	case "screenshot-tools":
		return fixtureengine.New(screenshottools.Definition, path, screenshottools.Factory)
	case "service-monitor":
		return fixtureengine.New(servicemonitor.Definition, path, servicemonitor.Factory)
	case "spotify":
		return fixtureengine.New(spotify.Definition, path, spotify.Factory)
	case "steam-library":
		return fixtureengine.New(steamlibrary.Definition, path, steamlibrary.Factory)
	case "system-monitor":
		return fixtureengine.New(systemmonitor.Definition, path, systemmonitor.Factory)
	case "timers-pomodoro":
		return fixtureengine.New(timerspomodoro.Definition, path, timerspomodoro.Factory)
	case "weather":
		return fixtureengine.New(weather.Definition, path, weather.Factory)
	}
	panic(fmt.Sprintf("unknown fixture plugin %q", slug))
}
