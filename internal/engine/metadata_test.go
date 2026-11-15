package engine

import (
	"reflect"
	"testing"
)

// TestAdditiveActionSemantics verifies additive action semantics
func TestAdditiveActionSemantics(t *testing.T) {
	cases := map[string][]string{"obs-studio": {"volume"}, "home-assistant": {"brightness"}, "vlc": {"volume", "seek"}, "spotify": {"volume", "seek"}, "counter-tally": {"increment", "decrement", "reset"}, "timers-pomodoro": nil}
	actions := []string{"volume", "seek", "brightness", "increment", "decrement", "reset", "adjust"}
	for slug, expected := range cases {
		def := Definition{Slug: slug, Actions: actions}
		if slug == "counter-tally" {
			def.Actions = []string{"increment", "decrement", "reset"}
		}
		got := AdditiveActions(def)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("%s: %v != %v", slug, got, expected)
		}
		for _, action := range def.Actions {
			if AdditiveDial(slug, action) != AdditiveDial(slug, ID(slug)+"."+action) {
				t.Fatalf("qualified action %s", action)
			}
		}
		if len(got) > 0 {
			got[0] = "mutated"
			if reflect.DeepEqual(got, AdditiveActions(def)) {
				t.Fatal("aliased action inventory")
			}
		}
	}
}
