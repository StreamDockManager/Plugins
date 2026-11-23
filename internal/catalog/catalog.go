package catalog

import (
	homeassistant "github.com/StreamDockManager/Plugins/internal/plugins/home-assistant"
	makeplugin "github.com/StreamDockManager/Plugins/internal/plugins/make"
	mqtt "github.com/StreamDockManager/Plugins/internal/plugins/mqtt"
	obsstudio "github.com/StreamDockManager/Plugins/internal/plugins/obs-studio"
	osrshiscores "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	randommeme "github.com/StreamDockManager/Plugins/internal/plugins/random-meme"
	retroachievements "github.com/StreamDockManager/Plugins/internal/plugins/retroachievements"
	runescapehiscores "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
	tailscale "github.com/StreamDockManager/Plugins/internal/plugins/tailscale"
	vlc "github.com/StreamDockManager/Plugins/internal/plugins/vlc"

	"fmt"
	vaultexample "github.com/StreamDockManager/Plugins/internal/plugins/sdm-vault-example"
	"regexp"
	"strings"

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

type Definition = engine.Definition

// Definitions aggregates feature-owned metadata in the original official-plugin order
var registrations = []engine.Registration{
	{Definition: obsstudio.Definition, PluginFields: obsstudio.PluginFields, ActionFields: obsstudio.ActionFields},
	{Definition: homeassistant.Definition, PluginFields: homeassistant.PluginFields, ActionFields: homeassistant.ActionFields},
	{Definition: randommeme.Definition, PluginFields: randommeme.PluginFields, ActionFields: randommeme.ActionFields},
	{Definition: countertally.Definition, PluginFields: countertally.PluginFields, ActionFields: countertally.ActionFields},
	{Definition: timerspomodoro.Definition, PluginFields: timerspomodoro.PluginFields, ActionFields: timerspomodoro.ActionFields},
	{Definition: systemmonitor.Definition, PluginFields: systemmonitor.PluginFields, ActionFields: systemmonitor.ActionFields},
	{Definition: screenshottools.Definition, PluginFields: screenshottools.PluginFields, ActionFields: screenshottools.ActionFields},
	{Definition: weather.Definition, PluginFields: weather.PluginFields, ActionFields: weather.ActionFields},
	{Definition: randomizerdice.Definition, PluginFields: randomizerdice.PluginFields, ActionFields: randomizerdice.ActionFields},
	{Definition: batterypower.Definition, PluginFields: batterypower.PluginFields, ActionFields: batterypower.ActionFields},
	{Definition: networkvpn.Definition, PluginFields: networkvpn.PluginFields, ActionFields: networkvpn.ActionFields},
	{Definition: spotify.Definition, PluginFields: spotify.PluginFields, ActionFields: spotify.ActionFields},
	{Definition: microphonecontrols.Definition, PluginFields: microphonecontrols.PluginFields, ActionFields: microphonecontrols.ActionFields},
	{Definition: httpwebhooks.Definition, PluginFields: httpwebhooks.PluginFields, ActionFields: httpwebhooks.ActionFields},
	{Definition: servicemonitor.Definition, PluginFields: servicemonitor.PluginFields, ActionFields: servicemonitor.ActionFields},
	{Definition: internetspeedtest.Definition, PluginFields: internetspeedtest.PluginFields, ActionFields: internetspeedtest.ActionFields},
	{Definition: steamlibrary.Definition, PluginFields: steamlibrary.PluginFields, ActionFields: steamlibrary.ActionFields},
	{Definition: vlc.Definition, PluginFields: vlc.PluginFields, ActionFields: vlc.ActionFields},
	{Definition: tailscale.Definition, PluginFields: tailscale.PluginFields, ActionFields: tailscale.ActionFields},
	{Definition: mqtt.Definition, PluginFields: mqtt.PluginFields, ActionFields: mqtt.ActionFields},
	{Definition: makeplugin.Definition, PluginFields: makeplugin.PluginFields, ActionFields: makeplugin.ActionFields},
	{Definition: osrshiscores.Definition, PluginFields: osrshiscores.PluginFields, ActionFields: osrshiscores.ActionFields},
	{Definition: runescapehiscores.Definition, PluginFields: runescapehiscores.PluginFields, ActionFields: runescapehiscores.ActionFields},
	{Definition: retroachievements.Definition, PluginFields: retroachievements.PluginFields, ActionFields: retroachievements.ActionFields},
	{Definition: vaultexample.Definition, PluginFields: vaultexample.PluginFields, ActionFields: vaultexample.ActionFields},
}

var Definitions, settingsRegistry = registrationIndex(registrations)

// registrationIndex indexes plugin registrations by their declared identity
func registrationIndex(entries []engine.Registration) ([]Definition, map[string]engine.Registration) {
	definitions := make([]Definition, 0, len(entries))
	index := make(map[string]engine.Registration, len(entries))
	for _, entry := range entries {
		if entry.Definition.Slug == "" || entry.PluginFields == nil || entry.ActionFields == nil {
			panic("incomplete plugin registration")
		}
		if _, exists := index[entry.Definition.Slug]; exists {
			panic("duplicate plugin registration: " + entry.Definition.Slug)
		}
		definitions = append(definitions, entry.Definition)
		index[entry.Definition.Slug] = entry
	}
	return definitions, index
}

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ValidateDefinitions checks registrations for invalid or conflicting definitions
func ValidateDefinitions(definitions []Definition) error {
	if len(definitions) == 0 {
		return fmt.Errorf("plugin catalogue is empty")
	}
	seen := map[string]bool{}
	for _, d := range definitions {
		if d.Slug == "" || seen[d.Slug] {
			return fmt.Errorf("missing or duplicate plugin slug %q", d.Slug)
		}
		seen[d.Slug] = true
		if !stableVersion.MatchString(d.Version) {
			return fmt.Errorf("%s: plugin version must be canonical stable semver", d.Slug)
		}
		if !stableVersion.MatchString(d.MinimumManager) {
			return fmt.Errorf("%s: minimum Manager version must be canonical stable semver", d.Slug)
		}
		if len(d.ViewTargets) > 0 {
			if err := engine.ValidateViewTargets(d); err != nil {
				return err
			}
		}
		if len(d.Actions) == 0 {
			return fmt.Errorf("%s: plugin has no actions", d.Slug)
		}
		actions := map[string]bool{}
		for _, action := range d.Actions {
			visual := VisualFor(d.Slug, action)
			if action == "" || actions[action] || visual.Icon == "" {
				return fmt.Errorf("%s: missing, duplicate or unillustrated action %q", d.Slug, action)
			}
			actions[action] = true
			for _, icon := range append([]string{visual.Icon}, visual.States...) {
				if d.Slug == "spotify" {
					if len(SpotifyAssets()[icon+".svg"]) == 0 {
						return fmt.Errorf("%s: missing icon %q", d.Slug, icon)
					}
				} else if actionGlyph(d.Slug, icon) == "" {
					return fmt.Errorf("%s: missing icon %q", d.Slug, icon)
				}
			}
		}
	}
	return nil
}

// ID constructs the fully qualified plugin identity
func ID(slug string) string { return engine.ID(slug) }

// ActionName resolves the display name for an action identity
func ActionName(slug, action string) string {
	if slug == vaultexample.Slug {
		return vaultexample.ActionName(action)
	}
	if slug == "randomizer-dice" && action == "heads-or-tails" {
		return "Heads or Tails"
	}
	if slug == "randomizer-dice" && action == "roll-all" {
		return "Roll All"
	}
	if slug == "spotify" && action == "mute-toggle" {
		return "Mute Toggle"
	}
	if slug == "random-meme" {
		if action == "send" {
			return "Paste link"
		}
		if action == "copy" {
			return "Copy link"
		}
	}
	return strings.ToUpper(action[:1]) + action[1:]
}
