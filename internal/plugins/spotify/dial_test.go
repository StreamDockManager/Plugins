package spotify

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/native/spotify"
)

// Verify adjustment actions ignore keys and touch
func TestAdjustmentActionsIgnoreKeysAndTouch(t *testing.T) {
	e := New("")
	e.spotify = func(context.Context, string, map[string]any) (string, error) {
		t.Fatal("non-dial player write")
		return "", nil
	}
	for _, action := range []string{"volume", "seek"} {
		for _, kind := range []string{"keyDown", "keyUp", "touchTap", "dialDown", "dialUp"} {
			if err := e.handleAction(context.Background(), nil, sdk.Event{Event: kind, Action: "spotify." + action}, binding{Action: "spotify." + action}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Verify dial adjustments use signed delta without changing settings
func TestDialAdjustmentsUseSignedDeltaWithoutChangingSettings(t *testing.T) {
	e := New("")
	for _, a := range []string{"volume", "seek"} {
		if !e.BatchDial("app.streamdockmanager.plugins.spotify." + a) {
			t.Fatal("relative rotation did not opt into bounded additive batching", a)
		}
	}
	if e.BatchDial("app.streamdockmanager.plugins.spotify.toggle") {
		t.Fatal("transport cannot batch rotation")
	}
	settings := map[string]any{"step": 5.0, "volume": 50.0, "position": 0.0}
	for _, action := range []string{"volume", "seek"} {
		for _, ticks := range []int{-2, 3} {
			ev := sdk.Event{Event: "dialRotate", Payload: json.RawMessage([]byte(`{"ticks":` + jsonNumber(ticks) + `}`))}
			values, ok := dialAdjustment(action, ev, settings)
			if !ok || values["delta"] != float64(ticks)*5 || len(values) != 1 {
				t.Fatal(action, values, ok)
			}
		}
		for _, event := range []string{"keyDown", "dialDown", "touchTap", "keyUp"} {
			if _, ok := dialAdjustment(action, sdk.Event{Event: event}, settings); ok {
				t.Fatal("non-rotation adjusted player", event)
			}
		}
	}
	if settings["volume"] != 50.0 || settings["position"] != 0.0 || len(settings) != 3 {
		t.Fatal("settings mutated", settings)
	}
	if _, ok := dialAdjustment("toggle", sdk.Event{Event: "dialRotate"}, settings); ok {
		t.Fatal("transport accepted rotation")
	}
}

// Decode a numeric feedback field without losing its JSON representation
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }

// Verify companion displays show current values
func TestCompanionDisplaysShowCurrentValues(t *testing.T) {
	volume, position, duration := 37.0, 45.0, 180.0
	status := spotify.SpotifyStatus{Mode: "playing", VolumePercent: &volume, PositionSeconds: &position, DurationSeconds: &duration, Title: "Current song", Artist: "Current artist"}
	for _, width := range []int{112, 176, 800} {
		surface := &sdk.Surface{Width: width, Height: 112}
		value := func(field any) any {
			if properties, ok := field.(map[string]any); ok {
				return properties["value"]
			}
			return field
		}
		v := PlaybackView(binding{Action: "spotify.volume-display", Surface: surface}, status)
		if value(v.Feedback["indicator"]) != 37.0 || value(v.Feedback["value"]) != "37%" {
			t.Fatal(v)
		}
		p := PlaybackView(binding{Action: "spotify.progress", Surface: surface}, status)
		if value(p.Feedback["indicator"]) != 25.0 || value(p.Feedback["time"]) != "00:45 / 03:00" {
			t.Fatal(p)
		}
		for action, companion := range map[string]spotifyDisplay{"volume": v, "seek": p} {
			knob := PlaybackView(binding{Action: "spotify." + action, Surface: surface}, status)
			if knob.Layout != companion.Layout || !reflect.DeepEqual(knob.Feedback, companion.Feedback) {
				t.Fatal("dial display diverged from companion readout", action, knob)
			}
		}
		info := PlaybackView(binding{Action: "spotify.track-info", Surface: surface}, status)
		if info.Feedback["title"] != status.Title || info.Feedback["artist"] != status.Artist {
			t.Fatal(info)
		}
	}
	unavailable := PlaybackView(binding{Action: "spotify.volume-display"}, spotify.SpotifyStatus{Mode: "unavailable"})
	if unavailable.Feedback["value"] != "--%" || unavailable.Feedback["indicator"] != 0.0 {
		t.Fatal("stale volume", unavailable)
	}
}
