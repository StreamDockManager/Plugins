package spotify

import (
	"context"
	"encoding/json"
	"errors"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	native "github.com/StreamDockManager/Plugins/internal/native/spotify"
	"sync"
	"testing"
)

func TestMuteToggleRestoresSuccessfulVolumeAndPreservesFailure(t *testing.T) {
	e := New("")
	volume := 37.0
	fail := false
	writes := 0
	e.spotify = func(_ context.Context, action string, settings map[string]any) (string, error) {
		if action == "status" {
			b, _ := json.Marshal(native.SpotifyStatus{Mode: "playing", VolumePercent: &volume})
			return string(b), nil
		}
		if action != "volume" {
			t.Fatal(action)
		}
		writes++
		if fail {
			return "", errors.New("write failed")
		}
		volume = settings["volume"].(float64)
		return "", nil
	}
	if err := e.toggleMute(context.Background()); err != nil || volume != 0 || e.spotifyRestoreVolume != 37 {
		t.Fatal(err, volume)
	}
	if err := e.toggleMute(context.Background()); err != nil || volume != 37 {
		t.Fatal(err, volume)
	}
	volume = 70
	fail = true
	if err := e.toggleMute(context.Background()); err == nil || e.spotifyRestoreVolume != 37 {
		t.Fatal("failed mute changed restore state")
	}
	fail = false
	volume = 0
	e.spotifyRestoreVolume = 0
	if err := e.toggleMute(context.Background()); err != nil || volume != 50 {
		t.Fatal("initial muted fallback", err, volume)
	}
	if writes != 4 {
		t.Fatal(writes)
	}
}

func TestMuteToggleUnavailableAndNonActivatingEventsDoNotWrite(t *testing.T) {
	e := New("")
	reads := 0
	e.spotify = func(_ context.Context, a string, _ map[string]any) (string, error) {
		if a != "status" {
			t.Fatal("unexpected write")
		}
		reads++
		return `{"mode":"unavailable"}`, nil
	}
	if err := e.toggleMute(context.Background()); err == nil {
		t.Fatal("unknown volume must fail")
	}
	for _, kind := range []string{"dialUp", "dialRotate", "keyUp"} {
		if err := e.handleAction(context.Background(), nil, sdk.Event{Event: kind, Action: "spotify.mute-toggle"}, binding{Action: "spotify.mute-toggle"}); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Fatal("nonactivating event read volume", reads)
	}
}

func TestMuteDialPressActivatesOnceAndReleaseDoesNot(t *testing.T) {
	e := New("")
	writes := 0
	e.spotify = func(_ context.Context, a string, _ map[string]any) (string, error) {
		if a == "status" {
			return `{"mode":"playing","volume_percent":37}`, nil
		}
		if a != "volume" {
			t.Fatal(a)
		}
		writes++
		return "", nil
	}
	// No physical binding is installed; verify exactly one write for the press pair.
	for _, event := range []string{"dialDown", "dialUp"} {
		if err := e.handleAction(context.Background(), nil, sdk.Event{Event: event, Action: "spotify.mute-toggle"}, binding{Action: "spotify.mute-toggle"}); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatal("press/release writes", writes)
	}
}

func TestMuteConcurrentPressesSerializeForLocalSpotifyTarget(t *testing.T) {
	e := New("")
	volume := 37.0
	writes := 0
	e.spotify = func(_ context.Context, a string, s map[string]any) (string, error) {
		if a == "status" {
			b, _ := json.Marshal(native.SpotifyStatus{Mode: "playing", VolumePercent: &volume})
			return string(b), nil
		}
		volume = s["volume"].(float64)
		writes++
		return "", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.toggleMute(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if writes != 20 || volume != 37 {
		t.Fatal("concurrent toggles lost restoration", writes, volume)
	}
}
