package spotify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/native/spotify"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Verify icons keep square proportions across display sizes
func TestIconsKeepSquareProportionsAcrossDisplaySizes(t *testing.T) {
	for _, size := range [][2]int{{64, 64}, {80, 80}, {85, 85}, {96, 96}, {100, 100}, {112, 112}, {176, 112}} {
		surface := &sdk.Surface{Width: size[0], Height: size[1]}
		e := New(filepath.Join(t.TempDir(), "state.json"))
		b := binding{Action: "mic.toggle", Surface: surface}
		e.Bindings["key"] = b
		generic := e.DisplayView("key", displayStatus{Text: "Muted", State: 1})
		spotify := PlaybackView(binding{Action: "spotify.toggle", Surface: surface}, spotify.SpotifyStatus{Mode: "playing"})
		for _, feedback := range []map[string]any{generic.Feedback, spotify.Feedback} {
			rect := feedback["icon"].(map[string]any)["rect"].([4]int)
			width, height := rect[2]*size[0]/200, rect[3]*size[1]/100
			if width-height > 2 || height-width > 2 || width < 20 || rect[0]+rect[2] > 200 || rect[1]+rect[3] > 100 {
				t.Fatal("distorted or clipped icon", size, rect)
			}
		}
		cover := ArtworkView(binding{Surface: surface}).Feedback["art"].(map[string]any)
		rect := cover["rect"].([4]int)
		width, height := rect[2]*size[0]/200, rect[3]*size[1]/100
		if abs := width - height; abs < -2 || abs > 2 {
			t.Fatal("album artwork was stretched", size, rect)
		}
	}
}

// Verify official displays only send changed fields
func TestOfficialDisplaysOnlySendChangedFields(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			e.Bindings["key"] = binding{Action: definition.Actions[0]}
			e.Bindings["touch"] = binding{Action: definition.Actions[0], Surface: &sdk.Surface{Width: 176, Height: 112}}
			render := func(id, title string, indicator float64) displayStep {
				return displayStep{Render: func(ctx context.Context, c *sdk.Client) error {
					view := e.DisplayView(id, displayStatus{Text: title})
					view.Feedback = cloneFeedback(view.Feedback)
					view.Feedback["indicator"] = indicator
					return e.SendDisplayView(ctx, c, id, view, true)
				}}
			}
			all := driveDisplays(t, e, []displayStep{
				render("key", "Ready", 0),
				render("touch", "Ready", 0),
				render("key", "Updated", 0),
				render("key", "Updated", 0),
				render("key", "Updated", 50),
			})
			requireUpdatedContexts(t, all[2], "key")
			if len(all[2]) != 2 || all[2][0].Event != "setTitle" || len(visualPayload(all[2], "key", "setFeedback")) != 1 {
				t.Fatal("text update resent unchanged fields", all[2])
			}
			if len(all[3]) != 0 {
				t.Fatal("unchanged display was resent", all[3])
			}
			if len(all[4]) != 1 || visualPayload(all[4], "key", "setFeedback")["indicator"] != float64(50) || len(visualPayload(all[4], "key", "setFeedback")) != 1 {
				t.Fatal("progress update resent title, icon, or other feedback", all[4])
			}
		})
	}
}

var displayEvent = testhost.DisplayEvent

// Drive Spotify fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested Spotify fixture step and signal its completion
func (d *displayDriver) Handle(ctx context.Context, c *sdk.Client, event sdk.Event) error {
	var index int
	if err := json.Unmarshal(event.Payload, &index); err != nil {
		return err
	}
	step := d.steps[index]
	if step.before != nil {
		step.before()
	}
	if step.event.Event == "" {
		for id, status := range d.Statuses {
			status.Next = time.Time{}
			d.Statuses[id] = status
		}
	}
	var err error
	if step.Render != nil {
		err = step.Render(ctx, c)
	} else if step.event.Event == "" {
		err = d.Engine.Poll(ctx, c)
	} else {
		err = d.Engine.Handle(ctx, c, step.event)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending := false
		for id := range d.Jobs {
			pending = pending || strings.HasPrefix(id, "status:")
		}
		if !pending {
			break
		}
		if result := d.DrainJobs(ctx, c); result != nil {
			err = result
			break
		}
		time.Sleep(time.Millisecond)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return c.Send(ctx, sdk.Message("fixtureDone", "", message))
}

// Keep the Spotify display fixture idle between explicit test steps
func (d *displayDriver) Poll(context.Context, *sdk.Client) error { return nil }

type displayDriver struct {
	*Engine
	steps []displayStep
}

type displayStep struct {
	event  sdk.Event
	before func()
	Render func(context.Context, *sdk.Client) error
}

var visualPayload = testhost.VisualPayload

// Verify artwork is bounded and normalized
func TestArtworkIsBoundedAndNormalized(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 512, 128))
	var encoded bytes.Buffer
	if e := png.Encode(&encoded, im); e != nil {
		t.Fatal(e)
	}
	result, e := artwork(context.Background(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(encoded.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	_, payload, _ := strings.Cut(result, ",")
	data, e := base64.StdEncoding.DecodeString(payload)
	if e != nil {
		t.Fatal(e)
	}
	config, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width != 256 || config.Height != 64 {
		t.Fatal(config, e)
	}
	for _, source := range []string{"http://example.invalid/artwork.png", "data:image/png;base64,not-base64"} {
		if _, e := artwork(context.Background(), source); e == nil {
			t.Fatal("accepted invalid artwork", source)
		}
	}
}

// Verify device notifications do not create action contexts
func TestDeviceNotificationsDoNotCreateActionContexts(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			for _, kind := range []string{"deviceDidConnect", "deviceDidDisconnect", "systemDidWakeUp", "didReceiveGlobalSettings"} {
				if err := e.Handle(context.Background(), nil, sdk.Event{Event: kind, Device: "simulator:n4-pro"}); err != nil {
					t.Fatal(kind, err)
				}
				if len(e.Bindings) != 0 || len(e.State) != 0 {
					t.Fatalf("%s created a phantom binding or state without an action context", kind)
				}
			}

			if err := e.Poll(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Verify all official plugins release retired contexts without creating saved state
func TestAllOfficialPluginsReleaseRetiredContextsWithoutCreatingSavedState(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			e := New(path)
			for i := range 1000 {
				id := fmt.Sprint(i)
				e.Bindings[id] = binding{Action: definition.Actions[0]}
				e.ResultsText[id] = "Finished"
				e.Results[id] = make([]byte, 1024)
				e.Checked[id] = time.Now()
				e.EffectiveSettings(id, nil)
				e.Last[id] = e.DisplayView(id, displayStatus{Text: "Ready"})
				if err := e.Handle(context.Background(), nil, sdk.Event{Event: "willDisappear", Context: id}); err != nil {
					t.Fatal(err)
				}
			}
			if len(e.State)+len(e.Bindings)+len(e.Results)+len(e.ResultsText)+len(e.Checked)+len(e.Last)+len(e.ProbeKeys)+len(e.Jobs)+len(e.Effective) != 0 {
				t.Fatal("retired contexts retained state")
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("display-only lifecycle wrote persistent state", err)
			}
		})
	}
}

// Verify official plugin display lifecycle matrix
func TestOfficialPluginDisplayLifecycleMatrix(t *testing.T) {
	for _, definition := range Definitions {
		for _, count := range []int{1, 32} {
			t.Run(fmt.Sprintf("%s/%d", definition.Slug, count), func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
				e.spotify = func(context.Context, string, map[string]any) (string, error) { return `{"mode":"paused"}`, nil }
				e.observeSpotify = nil
				var steps []displayStep
				for i := range count {
					steps = append(steps, displayStep{event: displayEvent("willAppear", fmt.Sprint(i), definition.Actions[0])})
				}
				for range 3 {
					steps = append(steps, displayStep{Render: func(ctx context.Context, c *sdk.Client) error { return e.Poll(ctx, c) }})
				}

				steps = append(steps, displayStep{Render: func(ctx context.Context, c *sdk.Client) error {
					e.startJob(ctx, "fixture-activation", func(context.Context) (string, any, error) { return "Finished", nil, nil })
					select {
					case <-e.WakeChannel:
					case <-time.After(time.Second):
						return fmt.Errorf("activation did not finish")
					}
					return e.DrainJobs(ctx, c)
				}})
				for i := range count {
					id := fmt.Sprint(i)
					steps = append(steps, displayStep{event: displayEvent("didReceiveSettings", id, definition.Actions[0])}, displayStep{event: displayEvent("willDisappear", id, definition.Actions[0])}, displayStep{event: displayEvent("willAppear", id, definition.Actions[0])}, displayStep{event: displayEvent("willDisappear", id, definition.Actions[0])})
				}
				driveDisplays(t, e, steps)
				if len(e.Bindings)+len(e.ProbeKeys)+len(e.Samples)+len(e.Jobs)+len(e.Last)+len(e.LastStatus) != 0 {
					t.Fatal("lifecycle/shutdown retained transient state")
				}
			})
		}
	}
}

// Measure official plugins
func BenchmarkOfficialPlugins(b *testing.B) {
	for _, definition := range Definitions {
		b.Run(definition.Slug, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "state.json")
			b.Run("construct", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					e := New(path)
					_ = e.Close()
				}
			})
			for _, count := range []int{0, 1, 32} {
				b.Run(fmt.Sprintf("schedule-%d", count), func(b *testing.B) {
					e := New(path)
					defer e.Close()
					for i := range count {
						e.Bindings[fmt.Sprint(i)] = binding{Action: definition.Actions[0], Settings: map[string]any{}}
					}
					now := time.Now()
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						_ = e.NextPoll(now)
					}
				})
			}
			b.Run("unchanged-display-32", func(b *testing.B) {
				e := New(path)
				defer e.Close()
				ids := make([]string, 32)
				for i := range ids {
					ids[i] = fmt.Sprint(i)
					e.Bindings[ids[i]] = binding{Action: definition.Actions[0]}
					view := e.DisplayView(ids[i], displayStatus{Text: "Ready"})
					e.Last[ids[i]] = view
					e.LastStatus[ids[i]] = displayStatus{Text: "Ready"}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					for _, id := range ids {
						if err := e.SendDisplay(context.Background(), nil, id, displayStatus{Text: "Ready"}); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
			b.Run("context-churn-1000", func(b *testing.B) {
				e := New(path)
				defer e.Close()
				ids := make([]string, 1000)
				for i := range ids {
					ids[i] = fmt.Sprint(i)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					for _, id := range ids {
						e.Bindings[id] = binding{Action: definition.Actions[0]}
						if err := e.Handle(context.Background(), nil, sdk.Event{Event: "willDisappear", Context: id}); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		})
	}
}

// Assert that only the expected binding contexts received display updates
func requireUpdatedContexts(t *testing.T, events []sdk.Event, ids ...string) {
	t.Helper()
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = false
	}
	for _, event := range events {
		switch event.Event {
		case "setTitle", "setState", "setImage", "setFeedback", "setFeedbackLayout":
			if _, ok := wanted[event.Context]; !ok {
				t.Fatalf("unrelated context %q received %s: %s", event.Context, event.Event, event.Payload)
			}
			wanted[event.Context] = true
		}
	}
	for id, found := range wanted {
		if !found {
			t.Errorf("context %q did not receive an update", id)
		}
	}
}

// Verify Spotify observer failure restores polling and backs off registration
func TestSpotifyObserverFailureRestoresPollingAndBacksOffRegistration(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.Now()
	o := &fixtureMediaObserver{}
	e.observeSpotify = func(chan<- struct{}) spotify.MediaObserver { return o }
	e.Bindings["status"] = binding{Action: "spotify.status"}
	e.syncSpotifyObserver(now)
	o.done.Store(true)
	e.syncSpotifyObserver(now)
	if o.closed != 1 || e.spotifyObserver != nil || !e.spotifyObserverRetry.Equal(now.Add(30*time.Second)) || e.spotifyNext.After(now.Add(250*time.Millisecond)) {
		t.Fatal("failure did not restore bounded polling")
	}
	e.syncSpotifyObserver(now.Add(time.Second))
	if e.spotifyObserver != nil {
		t.Fatal("failed registration retried every tick")
	}
}

// Verify Spotify observer shares lease and coalesces without cancelling reads
func TestSpotifyObserverSharesLeaseAndCoalescesWithoutCancellingReads(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	o := &fixtureMediaObserver{}
	o.healthy.Store(true)
	o.revision.Store(1)
	subscriptions := 0
	e.observeSpotify = func(chan<- struct{}) spotify.MediaObserver { subscriptions++; return o }
	now := time.Now()
	e.Bindings["one"] = binding{Action: "spotify.status"}
	e.Bindings["two"] = binding{Action: "spotify.toggle"}
	e.Bindings["static"] = binding{Action: "spotify.next"}
	e.syncSpotifyObserver(now)
	e.syncSpotifyObserver(now)
	if subscriptions != 1 {
		t.Fatal("per-context observer", subscriptions)
	}
	cancelled := false
	e.Jobs["spotify-status"] = backgroundJob{Cancel: func() { cancelled = true }}
	e.spotifyObserved = now
	o.revision.Store(10000)
	e.syncSpotifyObserver(now.Add(time.Millisecond))
	if cancelled || !e.spotifyNext.Equal(now.Add(250*time.Millisecond)) {
		t.Fatal("notification storm cancelled or exceeded cadence", e.spotifyNext)
	}
	delete(e.Bindings, "one")
	e.syncSpotifyObserver(now)
	if o.closed != 0 {
		t.Fatal("observer dropped while toggle remained")
	}
	delete(e.Bindings, "two")
	e.artCache.Put("old", "old artwork", len("old artwork"), 0, 0)
	e.syncSpotifyObserver(now)
	if o.closed != 1 || !cancelled || e.spotifyObserver != nil {
		t.Fatal("last dynamic context leaked observer/probe")
	}
	if e.artCache.Len() != 0 || !e.spotifyObserved.IsZero() || !e.spotifyNext.IsZero() {
		t.Fatal("last dynamic context retained observation buffers or delayed new demand")
	}
	e.syncSpotifyObserver(now)
	if subscriptions != 1 {
		t.Fatal("static controls started native observation")
	}
}

// Verify Spotify position ticks are immutable and independent of native sampling
func TestSpotifyPositionTicksAreImmutableAndIndependentOfNativeSampling(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.Now()
	position, duration, rate := 10.0, 16.0, 2.0
	e.spotifyObserved = now
	e.spotifySample = spotify.SpotifyStatus{Mode: "playing", PositionSeconds: &position, DurationSeconds: &duration, Rate: &rate}
	e.spotifyNext = now.Add(10 * time.Second)
	e.NextPeriodic = now.Add(time.Second)
	e.Bindings["status"] = binding{Action: "spotify.status"}
	o := &fixtureMediaObserver{}
	o.healthy.Store(true)
	e.spotifyObserver = o
	if got := e.NextPoll(now); !got.Equal(now.Add(time.Second)) {
		t.Fatal("position tick waited for native sample", got)
	}
	one := e.spotifyObservedStatus(now.Add(2 * time.Second))
	two := e.spotifyObservedStatus(now.Add(4 * time.Second))
	if *one.PositionSeconds != 14 || *two.PositionSeconds != 16 || position != 10 {
		t.Fatal("position did not advance/clamp immutably", one, two, position)
	}
	e.spotifySample.Mode = "paused"
	if got := e.spotifyObservedStatus(now.Add(20 * time.Second)); *got.PositionSeconds != 10 {
		t.Fatal("paused position advanced", got)
	}
	if e.spotifyObservedStatus(now.Add(31*time.Second)).Mode != "unavailable" {
		t.Fatal("stale native sample retained")
	}
	o.healthy.Store(false)
	if e.spotifyObservedStatus(now.Add(4*time.Second)).Mode != "unavailable" {
		t.Fatal("failed observer advertised extended freshness")
	}
}

// Record closure of the fixture-owned resource
func (o *fixtureMediaObserver) Close() { o.closed++ }

// Read the fake media observer completion flag
func (o *fixtureMediaObserver) Done() bool { return o.done.Load() }

// Read the fake media observer health flag
func (o *fixtureMediaObserver) Healthy() bool { return o.healthy.Load() }

// Read the fake media observer revision counter
func (o *fixtureMediaObserver) Revision() uint64 { return o.revision.Load() }

type fixtureMediaObserver struct {
	revision atomic.Uint64
	healthy  atomic.Bool
	done     atomic.Bool
	closed   int
}

// Measure Spotify artwork display
func BenchmarkSpotifyArtworkDisplay(b *testing.B) {
	for _, count := range []int{1, 32} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			e := New(filepath.Join(b.TempDir(), "state.json"))
			defer e.Close()
			status := spotify.SpotifyStatus{Mode: "paused", Artwork: "https://fixture.invalid/cover.png"}
			art := "data:image/png;base64," + strings.Repeat("A", 64*1024)
			e.artCache.Put(fmt.Sprintf("spotify:%x", sha256.Sum256([]byte(status.Artwork))), art, len(art), 0, 0)
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprint(i)
				bind := binding{Action: "spotify.status"}
				e.Bindings[ids[i]] = bind
				view := PlaybackView(bind, status)
				view.Image = art
				view.Feedback["art"] = art
				seedMeasuredView(e, ids[i], view)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, id := range ids {
					if err := e.renderSpotify(context.Background(), nil, id, status); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// Verify Spotify lifecycle clears stale artwork and shares polling
func TestSpotifyLifecycleClearsStaleArtworkAndSharesPolling(t *testing.T) {
	var pngBytes bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{200, 40, 30, 255})
	if err := png.Encode(&pngBytes, im); err != nil {
		t.Fatal(err)
	}
	art := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	f := &spotifyFixture{Engine: New(filepath.Join(t.TempDir(), "state.json")), art: art,
		status: spotify.SpotifyStatus{Mode: "playing", PositionSeconds: new(45.9), DurationSeconds: new(214.8), Artwork: art}}
	f.spotify = func(_ context.Context, action string, settings map[string]any) (string, error) {
		if action != "status" {
			return "", fmt.Errorf("test must not actuate Spotify")
		}
		f.queries++
		if f.failed {
			return "", fmt.Errorf("Spotify is not running")
		}
		if !configvalue.Bool(settings, "include_artwork") {
			return "", fmt.Errorf("visible status did not request artwork")
		}
		data, err := json.Marshal(f.status)
		return string(data), err
	}
	f.observeSpotify = nil
	results := make(chan map[string][]sdk.Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		connection.SetReadLimit(1 << 20)
		wsCtx, wsCancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Second))
		defer wsCancel()
		var registration sdk.Event
		if err := wsjson.Read(wsCtx, connection, &registration); err != nil || registration.UUID != "fixture-token" {
			t.Error("registration failed", registration, err)
			return
		}
		for _, action := range []string{"status", "toggle", "next", "previous", "volume", "seek"} {
			err := wsjson.Write(wsCtx, connection, sdk.Event{Event: "willAppear", Context: action, Action: "spotify." + action,
				Payload: json.RawMessage(`{"settings":{}}`), Surface: &sdk.Surface{Width: 176, Height: 112}})
			if err != nil {
				t.Error(err)
				return
			}
		}
		all := map[string][]sdk.Event{}
		for _, phase := range []string{"initial", "tick", "paused", "unavailable", "recover"} {
			if err := wsjson.Write(wsCtx, connection, sdk.Event{Event: "fixturePoll", Context: phase}); err != nil {
				t.Error(err)
				return
			}
			for {
				var event sdk.Event
				if err := wsjson.Read(wsCtx, connection, &event); err != nil {
					t.Error(err)
					return
				}
				all[phase] = append(all[phase], event)
				if event.Event == "fixtureDone" {
					break
				}
			}
		}
		results <- all
		_ = wsjson.Write(wsCtx, connection, sdk.Event{Event: "shutdown"})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := testhost.Connect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "fixture-token", f); err != nil {
		t.Fatal(err)
	}
	all := <-results
	for phase, events := range all {
		var done map[string]int
		_ = json.Unmarshal(events[len(events)-1].Payload, &done)
		if done["queries"] != 1 {
			t.Fatal("poll was not shared across bindings", phase, done)
		}
		for _, event := range events {
			if event.Event == "setImage" && event.Context != "status" && strings.Contains(string(event.Payload), "data:image") {
				t.Fatal("control action received album artwork", event)
			}
		}
	}
	find := func(phase, context, kind string) map[string]any {
		for _, event := range slices.Backward(all[phase]) {

			if event.Context == context && event.Event == kind {
				var payload map[string]any
				_ = json.Unmarshal(event.Payload, &payload)
				return payload
			}
		}
		return nil
	}
	if find("initial", "toggle", "setImage")["image"] != "spotify/pause.svg" || find("paused", "toggle", "setImage")["image"] != "spotify/play.svg" {
		t.Fatal("toggle did not track the actual playback state", all)
	}
	if find("initial", "toggle", "setState")["state"] != float64(1) || find("paused", "toggle", "setState")["state"] != float64(0) {
		t.Fatal("manifest toggle state did not follow playback")
	}
	if image, ok := find("initial", "status", "setImage")["image"].(string); !ok || !strings.HasPrefix(image, "data:image/png;base64,") {
		t.Fatal("status did not receive album artwork")
	}
	if find("tick", "status", "setImage") != nil || find("tick", "status", "setFeedback")["art"] != nil || find("tick", "status", "setFeedback")["time"] != "00:46 / 03:34" {
		t.Fatal("time update resent artwork or lost timing", all["tick"])
	}
	requireUpdatedContexts(t, all["tick"], "status", "seek")
	time, _ := find("tick", "seek", "setFeedback")["time"].(map[string]any)
	if time["value"] != "00:46 / 03:34" {
		t.Fatal("dial progress did not advance with its paired screen", all["tick"])
	}
	if find("paused", "status", "setImage")["image"] != "spotify/status.svg" || find("unavailable", "status", "setFeedback")["mode"] != "Unavailable" {
		t.Fatal("stale artwork or playback mode survived a missing player/cover")
	}
	if find("recover", "status", "setFeedback")["time"] != "00:00 / 03:34" {
		t.Fatal("playback did not recover at the actual track start")
	}
}

// Verify Spotify views use action icons and separate status layouts
func TestSpotifyViewsUseActionIconsAndSeparateStatusLayouts(t *testing.T) {
	state := spotify.SpotifyStatus{Mode: "playing", PositionSeconds: new(45.9), DurationSeconds: new(214.8), Artwork: "https://example.invalid/cover"}
	for action, label := range map[string]string{"toggle": "Pause", "next": "Next", "previous": "Previous"} {
		view := PlaybackView(binding{Action: "spotify." + action}, state)
		if view.Title != label || view.Feedback["label"] != label || view.Layout != "spotify/control.json" || !strings.HasPrefix(view.Image, "spotify/") || view.Feedback["art"] != nil {
			t.Fatal(action, view)
		}
	}
	for mode, icon := range map[string]string{"playing": "pause", "paused": "play", "stopped": "play", "unknown": "toggle"} {
		view := PlaybackView(binding{Action: "spotify.toggle"}, spotify.SpotifyStatus{Mode: mode})
		if view.Image != "spotify/"+icon+".svg" {
			t.Fatal(mode, view)
		}
	}
	for _, surface := range []*sdk.Surface{nil, {Width: 112, Height: 112}, {Width: 176, Height: 112}} {
		view := PlaybackView(binding{Action: "spotify.status", Surface: surface}, state)
		if view.Title != "Playing\n00:45 / 03:34" || view.Feedback["mode"] != "Playing" || view.Feedback["time"] != "00:45 / 03:34" {
			t.Fatal(view)
		}
		if surface != nil && surface.Width == 176 && view.Layout != "spotify/status-wide.json" {
			t.Fatal("wide touch surface used a key layout", view)
		}
	}
	missing := PlaybackView(binding{Action: "spotify.status"}, spotify.SpotifyStatus{Mode: "unavailable"})
	if missing.Title != "Unavailable\n--:-- / --:--" {
		t.Fatal(missing)
	}
	zero := PlaybackView(binding{Action: "spotify.status"}, spotify.SpotifyStatus{Mode: "paused", PositionSeconds: new(float64(0)), DurationSeconds: new(float64(214))})
	if zero.Feedback["time"] != "00:00 / 03:34" {
		t.Fatal(zero)
	}
	long := PlaybackView(binding{Action: "spotify.status", Surface: &sdk.Surface{Width: 112, Height: 112}}, spotify.SpotifyStatus{Mode: "playing", PositionSeconds: new(float64(12345)), DurationSeconds: new(float64(23456))})
	if long.Feedback["time"] != "" || long.Feedback["position"] != "3:25:45" || long.Feedback["duration"] != "/ 6:30:56" {
		t.Fatal("long timings clipped instead of wrapping", long)
	}
	for _, tc := range []struct {
		position, duration *float64
		want               string
	}{
		{new(125.9), new(3599.9), "02:05 / 59:59"},
		{new(125.9), new(float64(3600)), "0:02:05 / 1:00:00"},
		{new(float64(90061)), nil, "25:01:01 / --:--"},
		{nil, new(float64(3600)), "--:-- / 1:00:00"},
	} {
		for _, name := range []string{"status", "progress", "seek"} {
			view := PlaybackView(
				binding{Action: "spotify." + name, Surface: &sdk.Surface{Width: 176, Height: 112}},
				spotify.SpotifyStatus{Mode: "paused", PositionSeconds: tc.position, DurationSeconds: tc.duration},
			)
			timing := view.Feedback["time"]
			if field, ok := timing.(map[string]any); ok {
				timing = field["value"]
			}
			if timing != tc.want || !strings.HasSuffix(view.Title, "\n"+tc.want) {
				t.Fatalf("%s timing = %v, title = %q, want %q", name, timing, view.Title, tc.want)
			}
		}
	}
}

// Verify status press toggles artwork only and remembers each binding
func TestStatusPressTogglesArtworkOnlyAndRemembersEachBinding(t *testing.T) {
	for _, input := range []string{"keyDown", "touchTap"} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			e := New(path)
			e.observeSpotify = nil
			e.spotify = func(context.Context, string, map[string]any) (string, error) {
				t.Error("display toggle queried or controlled Spotify")
				return "", fmt.Errorf("unexpected player call")
			}
			cover := func(red uint8) string {
				im := image.NewRGBA(image.Rect(0, 0, 2, 2))
				im.Set(0, 0, color.RGBA{R: red, A: 255})
				var data bytes.Buffer
				if err := png.Encode(&data, im); err != nil {
					t.Fatal(err)
				}
				return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
			}
			art, nextArt := cover(50), cover(200)
			setSample := func(source string) {
				e.spotifyObserved = time.Now()
				e.spotifyNext = time.Now().Add(time.Hour)
				e.spotifyArt = true
				e.spotifySample = spotify.SpotifyStatus{Mode: "playing", Artwork: source, PositionSeconds: new(float64(45)), DurationSeconds: new(float64(214))}
				e.artCache.Clear()
				e.artCache.Put(fmt.Sprintf("spotify:%x", sha256.Sum256([]byte(source))), source, len(source), 0, 0)
			}
			setSample(art)
			id := "key"
			if input == "touchTap" {
				id = "touch"
			}
			for _, context := range []string{id, "other"} {
				ev := displayEvent("willAppear", context, "spotify.status")
				e.Bindings[context] = binding{Action: ev.Action, Surface: ev.Surface}
			}
			press := displayEvent(input, id, "spotify.status")
			release := displayEvent("keyUp", id, "spotify.status")
			if input == "touchTap" {
				release = press
				release.Payload = json.RawMessage(`{"hold":true}`)
			}
			all := driveDisplays(t, e, []displayStep{
				{Render: func(ctx context.Context, c *sdk.Client) error { return e.pollSpotify(ctx, c) }},
				{event: press},
				{event: release},
				{before: func() { setSample(nextArt) }, Render: func(ctx context.Context, c *sdk.Client) error { return e.pollSpotify(ctx, c) }},
				{event: press},
				{event: press},
				{event: displayEvent("willDisappear", id, "spotify.status")},
				{event: displayEvent("willAppear", id, "spotify.status")},
				{before: func() { setSample(""); e.spotifySample.Mode = "unavailable" }, Render: func(ctx context.Context, c *sdk.Client) error { return e.pollSpotify(ctx, c) }},
			})
			if visualPayload(all[0], id, "setFeedback")["mode"] != "Playing" {
				t.Fatal("initial view omitted playback details", all[0])
			}
			if visualPayload(all[1], id, "setFeedbackLayout")["layout"] != "spotify/artwork.json" || visualPayload(all[1], id, "setTitle")["title"] != "" {
				t.Fatal("press did not replace status layout and clear title", all[1])
			}
			if feedback := visualPayload(all[1], id, "setFeedback"); len(feedback) != 1 || feedback["art"].(map[string]any)["value"] != art {
				t.Fatal("artwork-only view retained status overlays", feedback)
			}
			requireUpdatedContexts(t, all[1], id)
			if len(all[2]) != 0 {
				t.Fatal("release/hold toggled the view again", all[2])
			}
			if visualPayload(all[3], id, "setImage")["image"] != nextArt || visualPayload(all[3], id, "setFeedback")["art"].(map[string]any)["value"] != nextArt {
				t.Fatal("artwork stopped following the current track", all[3])
			}
			if visualPayload(all[4], id, "setFeedback")["mode"] != "Playing" || visualPayload(all[4], id, "setFeedback")["time"] != "00:45 / 03:34" {
				t.Fatal("second press did not restore current status", all[4])
			}
			requireUpdatedContexts(t, all[4], id)
			if visualPayload(all[7], id, "setFeedbackLayout")["layout"] != "spotify/artwork.json" {
				t.Fatal("page departure reset the selected view", all[7])
			}
			if visualPayload(all[8], id, "setImage")["image"] != "spotify/status.svg" || len(visualPayload(all[8], id, "setFeedback")) != 1 {
				t.Fatal("missing player retained a stale cover or restored text", all[8])
			}
			if err := e.Persist(); err != nil {
				t.Fatal(err)
			}
			restored := New(path)
			defer restored.Close()
			if len(restored.State) != 1 || !restored.State[id].ArtworkOnly {
				t.Fatal("per-binding choice did not survive restart", restored.State)
			}
		})
	}
}

// Execute the requested Spotify fixture step and signal its completion
func (f *spotifyFixture) Handle(ctx context.Context, c *sdk.Client, event sdk.Event) error {
	if event.Event != "fixturePoll" {
		return f.Engine.Handle(ctx, c, event)
	}
	wait := func() error {
		deadline := time.Now().Add(time.Second)
		for {
			if _, pending := f.Jobs["spotify-status"]; !pending {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("Spotify probe timeout")
			}
			if err := f.DrainJobs(ctx, c); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := wait(); err != nil {
		return err
	}
	switch event.Context {
	case "tick":
		f.status.PositionSeconds = new(46.2)
	case "paused":
		f.status.Mode, f.status.Artwork = "paused", ""
	case "unavailable":
		f.failed = true
	case "recover":
		f.failed = false
		f.status.Mode, f.status.Artwork = "playing", f.art
		f.status.PositionSeconds = new(float64(0))
	}
	before := f.queries
	f.spotifyNext = time.Time{}
	if err := f.Engine.Poll(ctx, c); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	return c.Send(ctx, sdk.Message("fixtureDone", event.Context, map[string]any{"queries": f.queries - before}))
}

type spotifyFixture struct {
	*Engine
	status  spotify.SpotifyStatus
	art     string
	queries int
	failed  bool
}
type Engine = Plugin

var Definitions = []engine.Definition{Definition}

// Seed a playback binding with known timing measurements
func seedMeasuredView(e *Engine, id string, view pluginDisplay) { e.Last[id] = view }

// Verify inspector departure does not clear visible playback
func TestInspectorDepartureDoesNotClearVisiblePlayback(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Bindings["visible"] = binding{Action: "spotify.status"}
	e.spotifySample = spotify.SpotifyStatus{Mode: "playing"}
	e.artCache.Put("cover", "image", len("image"), 0, 0)
	if err := e.Handle(context.Background(), nil, sdk.Event{Event: "propertyInspectorDidDisappear", Context: "inspector"}); err != nil {
		t.Fatal(err)
	}
	if art, _ := e.artCache.Peek("cover", 0); e.spotifySample.Mode != "playing" || art != "image" {
		t.Fatal("unrelated inspector cleared active playback")
	}
}

// Verify artwork retains JPEG and GIF support
func TestArtworkRetainsJPEGAndGIFSupport(t *testing.T) {
	for format, encoded := range map[string]string{"gif": "R0lGODlhAgACAIcAAAAAAAAARAAAiAAAzABEAABERABEiABEzACIAACIRACIiACIzADMAADMRADMiADMzADd3REREQAAVQAAmQAA3QBVAABVVQBMmQBJ3QCZAACZTACZmQCT3QDdAADdSQDdkwDungDu7iIiIgAAZgAAqgAA7gBmAABmZgBVqgBP7gCqAACqVQCqqgCe7gDuAADuTwD/VQD/qgD//zMzMwAAdwAAuwAA/wB3AAB3dwBduwBV/wC7AAC7XQC7uwCq/wD/AEQAREQAiEQAzEREAEREREREiEREzESIAESIRESIiESIzETMAETMRETMiETMzEQAAFUAAFUAVUwAmUkA3VVVAFVVVUxMmUlJ3UyZAEyZTEyZmUmT3UndAEndSUndk0nd3U/u7mYAAGYAZlUAqk8A7mZmAGZmZlVVqk9P7lWqAFWqVVWqqk+e7k/uAE/uT0/unlX/qlX//3cAAHcAd10Au1UA/3d3AHd3d11du1VV/127AF27XV27u1Wq/1X/AFX/VYgAiIgAzIhEAIhERIhEiIhEzIiIAIiIRIiIiIiIzIjMAIjMRIjMiIjMzIgAAIgARJkATJkAmZMA3ZlMAJlMTJlMmZNJ3ZmZAJmZTJmZmZOT3ZPdAJPdSZPdk5Pd3ZkAAKoAAKoAVaoAqp4A7qpVAKpVVapVqp5P7qqqAKqqVaqqqp6e7p7uAJ7uT57unp7u7qr//7sAALsAXbsAu6oA/7tdALtdXbtdu6pV/7u7ALu7Xbu7u6qq/6r/AKr/Var/qswAzMxEAMxERMxEiMxEzMyIAMyIRMyIiMyIzMzMAMzMRMzMiMzMzMwAAMwARMwAiN0Ak90A3d1JAN1JSd1Jk91J3d2TAN2TSd2Tk92T3d3dAN3dSd3dk93d3d0AAN0ASe4AT+4Anu4A7u5PAO5PT+5Pnu5P7u6eAO6eT+6enu6e7u7uAO7uT+7unu7u7u4AAP8AAP8AVf8Aqv8A//9VAP9VVf9Vqv9V//+qAP+qVf+qqv+q////AP//Vf//qv///ywAAAAAAgACAAAIBgDhARgYEAA7", "jpeg": "/9j/2wCEAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDIBCQkJDAsMGA0NGDIhHCEyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMv/AABEIAAIAAgMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/APAGZnYszFmY5JJySaSiigG76s//2Q=="} {
		t.Run(format, func(t *testing.T) {
			result, err := artwork(context.Background(), "data:image/"+format+";base64,"+encoded)
			if err != nil || !strings.HasPrefix(result, "data:image/png;base64,") {
				t.Fatal(result, err)
			}
		})
	}
}

// Shared observation failures report once and healthy observations clear failure episodes
func TestSpotifyObservationFailurePolicy(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	c, _ := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
	for _, id := range []string{"b", "a"} {
		e.Bindings[id] = binding{Action: engine.ID("spotify") + ".status"}
	}
	var observed []string
	recovered := map[string]bool{}
	e.Feature.ObserveFailure = func(_ context.Context, _ *sdk.Client, id string, _ error) { observed = append(observed, id) }
	e.Feature.FilterStatus = func(_ context.Context, _ *sdk.Client, id string, s displayStatus) displayStatus {
		recovered[id] = true
		return s
	}
	if err := e.applySpotify(context.Background(), c, jobResult{Value: spotifyProbe{}, Err: fmt.Errorf("private provider error")}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0] != "a" || len(recovered) != 0 {
		t.Fatal(observed, recovered)
	}
	if err := e.applySpotify(context.Background(), c, jobResult{Value: spotifyProbe{}}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || len(recovered) != 2 {
		t.Fatal(observed, recovered)
	}
}
