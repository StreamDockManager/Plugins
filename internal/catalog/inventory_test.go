package catalog

import (
	"slices"
	"strings"
	"testing"
)

// TestOfficialInventory verifies official inventory
func TestOfficialInventory(t *testing.T) {
	expected := strings.Fields(`obs-studio home-assistant battery-power counter-tally http-webhooks internet-speed-test make microphone-controls mqtt network-vpn osrs-hiscores random-meme randomizer-dice retroachievements runescape-hiscores screenshot-tools service-monitor spotify steam-library system-monitor tailscale timers-pomodoro vlc weather`)
	actual := make([]string, 0, len(Definitions))
	for _, d := range Definitions {
		actual = append(actual, d.Slug)
		if d.Version != "0.1.0" {
			t.Fatal("unreleased plugin version changed", d)
		}
	}
	slices.Sort(expected)
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		t.Fatalf("official inventory differs\ngot  %v\nwant %v", actual, expected)
	}
	for _, slug := range []string{"vlc"} {
		for _, d := range Definitions {
			if d.Slug != slug {
				continue
			}
			descriptor := Descriptor(d)
			for _, a := range d.Actions {
				types := descriptor.Actions[ID(slug)+"."+a].ControlTypes
				if a == "volume" || a == "seek" {
					if !slices.Equal(types, []string{"encoder"}) {
						t.Fatal(slug, a, types)
					}
				} else if !slices.Equal(types, []string{"button", "touch"}) {
					t.Fatal(slug, a, types)
				}
			}
		}
	}
}
