package servicemonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/httpclient"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

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

// Drive service monitor fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested service monitor fixture step and signal its completion
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

// Keep the service monitor display fixture idle between explicit test steps
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

// Verify device notifications do not create action contexts
func TestDeviceNotificationsDoNotCreateActionContexts(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			for _, kind := range []string{"deviceDidConnect", "deviceDidDisconnect", "systemDidWakeUp", "didReceiveGlobalSettings"} {
				if err := e.Handle(context.Background(), nil, sdk.Event{Event: kind, Device: "simulator:n4-pro"}); err != nil {
					t.Fatal(kind, err)
				}
				if len(e.Bindings) != 0 {
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
			if len(e.Bindings)+len(e.Results)+len(e.ResultsText)+len(e.Checked)+len(e.Last)+len(e.ProbeKeys)+len(e.Jobs)+len(e.Effective) != 0 {
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

// Verify deadline scheduling preserves hidden timers and configured checks
func TestDeadlineSchedulingPreservesHiddenTimersAndConfiguredChecks(t *testing.T) {
	now := time.Now()
	e := New(filepath.Join(t.TempDir(), "service.json"))
	defer e.Close()
	e.Bindings["a"] = binding{Action: "service.http", Settings: map[string]any{"update_interval_ms": float64(30000)}}
	e.polling["a"] = pollingState{Next: now.Add(30 * time.Second)}
	if next := e.NextPoll(now); !next.Equal(now.Add(30 * time.Second)) {
		t.Fatal("service retained heartbeat", next)
	}
	e.Bindings["a"] = binding{Action: "service.refresh"}
	delete(e.Effective, "a")
	if !e.NextPoll(now).IsZero() {
		t.Fatal("refresh button retained polling")
	}
}

// Verify official plugin display lifecycle matrix
func TestOfficialPluginDisplayLifecycleMatrix(t *testing.T) {
	for _, definition := range Definitions {
		for _, count := range []int{1, 32} {
			t.Run(fmt.Sprintf("%s/%d", definition.Slug, count), func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
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

// Verify service HTTP uses conditional status reads and retains retry after
func TestServiceHTTPUsesConditionalStatusReadsAndRetainsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authenticated" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture" || password != "service-secret" {
				t.Error("service URL authentication changed")
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/busy" {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("ETag", `"status"`)
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(304)
			return
		}

		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	defer server.Close()
	cache := newHTTPValidatorCache()
	settings := map[string]any{"url": server.URL}
	for i := range 2 {
		_, value, err := checkService(context.Background(), "http", settings, cache, true)
		result, ok := value.(serviceResult)
		if err != nil || !ok || !result.Available || (i == 1 && !result.Revalidated) {
			t.Fatal(value, err)
		}
	}
	settings["url"] = server.URL + "/busy"
	_, value, err := checkService(context.Background(), "http", settings, cache, true)
	result := value.(serviceResult)
	if err != nil || result.Available || result.HTTPStatus != 429 || result.RetryAfter.Before(time.Now().Add(55*time.Second)) {
		t.Fatal(result, err)
	}
	settings["url"] = strings.Replace(server.URL, "http://", "http://fixture:service-secret@", 1) + "/authenticated"
	_, value, err = checkService(context.Background(), "http", settings, cache, true)
	if err != nil || !value.(serviceResult).Available {
		t.Fatal("service URL authentication unavailable", value, err)
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

type Engine = Plugin

var Definitions = []engine.Definition{Definition}

var ParsePath = httpclient.ParsePath
var PathSuggestions = httpclient.PathSuggestions

// Verify service retry deadline stays internal
func TestServiceRetryDeadlineStaysInternal(t *testing.T) {
	data, err := json.Marshal(serviceResult{RetryAfter: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "RetryAfter") {
		t.Fatal(string(data))
	}
}

// Verify explicit off loads service once and allows manual refresh
func TestExplicitOffLoadsServiceOnceAndAllowsManualRefresh(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(`{"value":42}`)) }))
	defer server.Close()
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	event := displayEvent("willAppear", "off", "service.http")
	event.Payload, _ = json.Marshal(map[string]any{"settings": map[string]any{"url": server.URL, "update_interval_ms": 0}})
	settle := func(ctx context.Context, c *sdk.Client) error {
		if err := e.Poll(ctx, c); err != nil {
			return err
		}
		deadline := time.Now().Add(2 * time.Second)
		for len(e.Jobs) > 0 && time.Now().Before(deadline) {
			if err := e.DrainJobs(ctx, c); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		if len(e.Jobs) > 0 {
			t.Fatal("request did not finish")
		}
		return nil
	}
	press := event
	press.Event = "keyDown"
	driveDisplays(t, e, []displayStep{{event: event}, {Render: settle}, {Render: func(ctx context.Context, c *sdk.Client) error {
		if calls.Load() != 1 {
			t.Fatal("Off did not load once", calls.Load())
		}
		for range 3 {
			if err := e.Poll(ctx, c); err != nil {
				return err
			}
		}
		if len(e.Jobs) != 0 || !e.NextPoll(time.Now()).IsZero() {
			t.Fatal("Off scheduled periodic sampling")
		}
		return nil
	}}, {event: press}, {Render: settle}})
	if calls.Load() != 2 {
		t.Fatal("manual refresh did not run", calls.Load())
	}
}

// Verify settings reset preserves completed off peer
func TestSettingsResetPreservesCompletedOffPeer(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint("global=", global), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }))
			defer server.Close()
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			appear := displayEvent("willAppear", "peer", "service.http")
			appear.Payload, _ = json.Marshal(map[string]any{"settings": map[string]any{"url": server.URL, "update_interval_ms": 0}})
			settle := func(ctx context.Context, c *sdk.Client) error {
				if err := e.Poll(ctx, c); err != nil {
					return err
				}
				deadline := time.Now().Add(time.Second)
				for len(e.Jobs) > 0 && time.Now().Before(deadline) {
					if err := e.DrainJobs(ctx, c); err != nil {
						return err
					}
					time.Sleep(time.Millisecond)
				}
				if len(e.Jobs) > 0 {
					return fmt.Errorf("request did not settle")
				}
				return nil
			}
			edit := displayEvent("didReceiveSettings", "other", "service.refresh")
			if global {
				edit = sdk.Event{Event: "didReceiveGlobalSettings", Payload: json.RawMessage(`{"settings":{}}`)}
			}
			driveDisplays(t, e, []displayStep{{event: appear}, {Render: settle}, {Render: func(ctx context.Context, c *sdk.Client) error {
				if calls.Load() != 1 {
					return fmt.Errorf("initial requests = %d", calls.Load())
				}
				return nil
			}}, {event: edit}, {Render: settle}, {Render: settle}})
			want := int32(1)
			if global {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("requests = %d, want %d", calls.Load(), want)
			}
		})
	}
}
