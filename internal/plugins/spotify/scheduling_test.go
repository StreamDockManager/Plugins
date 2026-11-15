package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
)

// Create a host recorder for Spotify playback completion tests
func playbackRecorder(t *testing.T) *testkit.Recorder {
	t.Helper()
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1","plugin-logs-v1","action-results-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	return testkit.New(host)
}

// Find the correlated playback action receipt
func playbackReceipt(t *testing.T, events []sdk.Event, request string, success bool) {
	t.Helper()
	receipts, summaries := 0, 0
	for _, event := range events {
		switch event.Event {
		case "actionResult":
			var payload struct {
				RequestID string `json:"request_id"`
				Success   bool   `json:"success"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.RequestID != request || payload.Success != success {
				t.Fatalf("unexpected action receipt: %+v", payload)
			}
			receipts++
		case "logMessage":
			var payload struct{ Message, Stream string }
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(payload.Message, "Action app.streamdockmanager.plugins.spotify.") {
				if (payload.Stream == "stdout") != success {
					t.Fatal("incorrect action summary", payload)
				}
				summaries++
			}
		}
	}
	if receipts != 1 || summaries != 1 {
		t.Fatalf("action must finish exactly once: receipts=%d summaries=%d", receipts, summaries)
	}
}

// Verify playback actions complete independently of shared refresh
func TestPlaybackActionsCompleteIndependentlyOfSharedRefresh(t *testing.T) {
	for _, action := range []string{"next", "previous", "toggle", "volume", "seek", "status", "volume-display", "progress", "track-info"} {
		for _, statusFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/status_failure_%t", action, statusFailure), func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
				defer e.Close()
				e.observeSpotify = nil
				recorder := playbackRecorder(t)
				started := make(chan struct{}, 4)
				release := make(chan struct{})
				var reads atomic.Int32
				commands := 0
				e.spotify = func(ctx context.Context, operation string, _ map[string]any) (string, error) {
					if operation != "status" {
						commands++
						return "", nil
					}
					reads.Add(1)
					started <- struct{}{}
					select {
					case <-ctx.Done():
						return "", ctx.Err()
					case <-release:
						if statusFailure {
							return "", errors.New("status temporarily unavailable")
						}
						return `{"mode":"playing","title":"Updated song"}`, nil
					}
				}
				for _, companion := range []string{"toggle", "status", "volume-display", "progress", "track-info"} {
					e.Bindings[companion] = binding{Action: "app.streamdockmanager.plugins.spotify." + companion}
				}
				e.Bindings["control"] = binding{Action: "app.streamdockmanager.plugins.spotify." + action}
				activate := func(request, kind string) {
					t.Helper()
					event := sdk.Message(kind, "control", map[string]any{"ticks": 1})
					if action == "volume" || action == "seek" {
						event.Event = "dialRotate"
					}
					event.Action = e.Bindings["control"].Action
					event.Surface = &sdk.Surface{RequestID: request, Width: 112, Height: 112}
					if err := recorder.Replay(context.Background(), e.Engine, []sdk.Event{event}); err != nil {
						t.Fatal(err)
					}
					playbackReceipt(t, recorder.Drain(), request, true)
				}
				activate("first", "keyDown")
				if len(e.Jobs) != 0 || reads.Load() != 0 {
					t.Fatal("rendering started background status work")
				}
				now := time.Now()
				if next := e.NextPoll(now); next.IsZero() || next.After(now) {
					t.Fatal("playback did not schedule its background status refresh", next)
				}
				poll := func() {
					t.Helper()
					if err := recorder.Poll(context.Background(), e.Engine); err != nil {
						t.Fatal(err)
					}
					recorder.Drain()
				}
				poll()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("status refresh did not start")
				}
				generation := e.Jobs["spotify-status"].Generation
				for range 3 {
					poll()
				}
				if len(e.Jobs) != 1 || reads.Load() != 1 || e.Jobs["spotify-status"].Generation != generation {
					t.Fatal("multiple displays or polls duplicated the shared refresh")
				}
				// A second successful input may retire the older read, but cannot
				// change either playback command's receipt into a cancellation
				activate("second", "touchTap")
				close(release)
				poll()
				deadline := time.Now().Add(2 * time.Second)
				for len(e.Jobs) != 0 && time.Now().Before(deadline) {
					select {
					case <-e.Wake():
					case <-time.After(time.Millisecond):
					}
					poll()
				}
				if len(e.Jobs) != 0 {
					t.Fatal("status refresh did not finish")
				}
				wantMode := "playing"
				if statusFailure {
					wantMode = "unavailable"
				}
				if e.spotifySample.Mode != wantMode {
					t.Fatal("status result was not applied", e.spotifySample)
				}
				wantCommands := 2
				if action == "status" || action == "volume-display" || action == "progress" || action == "track-info" {
					wantCommands = 0
				}
				if commands != wantCommands {
					t.Fatal("playback command was lost or repeated", commands)
				}
				receipts, summaries := 0, 0
				for _, event := range recorder.History {
					if event.Event == "actionResult" {
						receipts++
					}
					if event.Event == "logMessage" && strings.Contains(string(event.Payload), "failed or cancelled") {
						t.Fatal("background refresh contaminated an action result", event)
					}
					if event.Event == "logMessage" && strings.Contains(string(event.Payload), "Action app.streamdockmanager.plugins.spotify.") {
						summaries++
					}
				}
				if receipts != 2 || summaries != 2 {
					t.Fatal("background work duplicated an action completion", receipts, summaries)
				}
			})
		}
	}
}

// Verify playback command errors still fail the action
func TestPlaybackCommandErrorsStillFailTheAction(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.observeSpotify = nil
	e.spotify = func(context.Context, string, map[string]any) (string, error) {
		return "", errors.New("playback denied")
	}
	recorder := playbackRecorder(t)
	event := sdk.Event{Event: "keyDown", Action: "app.streamdockmanager.plugins.spotify.next", Context: "control", Surface: &sdk.Surface{RequestID: "denied"}}
	if err := recorder.Replay(context.Background(), e.Engine, []sdk.Event{event}); err != nil {
		t.Fatal(err)
	}
	playbackReceipt(t, recorder.Drain(), "denied", false)
	if len(e.Jobs) != 0 {
		t.Fatal("failed playback started a background refresh")
	}
}
