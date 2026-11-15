package timerspomodoro

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	countertally "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
	internetspeedtest "github.com/StreamDockManager/Plugins/internal/plugins/internet-speed-test"
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

// Verify timer icons track running completion and restart
func TestTimerIconsTrackRunningCompletionAndRestart(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	all := driveDisplays(t, e, []displayStep{
		{event: displayEvent("willAppear", "key", "timer.countdown")},
		{event: displayEvent("keyDown", "key", "timer.countdown")},
		{before: func() { e.State["key"].Deadline -= 1000 }},
		{before: func() { e.State["key"].Deadline = time.Now().Add(-time.Second).UnixMilli() }},
		{event: displayEvent("keyDown", "key", "timer.countdown")},
		{before: func() {
			v := e.State["key"]
			v.Mode, v.Break, v.FocusMillis, v.BreakMillis = "pomodoro", false, 60000, 30000
			v.Deadline = time.Now().Add(-time.Second).UnixMilli()
		}},
	})
	requireDisplayIcon(t, all[0], "key", "icons/timer.svg", 0)
	requireDisplayIcon(t, all[1], "key", "icons/timer-pause.svg", 1)
	if visualPayload(all[2], "key", "setImage") != nil || visualPayload(all[2], "key", "setFeedback")["icon"] != nil {
		t.Fatal("a timer tick resent the icon")
	}
	requireDisplayIcon(t, all[3], "key", "icons/check.svg", 2)
	requireDisplayIcon(t, all[4], "key", "icons/timer-pause.svg", 1)
	if visualPayload(all[4], "key", "setTitle")["title"] == "Done" {
		t.Fatal("completed status survived a timer restart")
	}
	requireDisplayIcon(t, all[5], "key", "icons/coffee.svg", 3)
}

var displayEvent = testhost.DisplayEvent

// Drive timers pomodoro fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested timers pomodoro fixture step and signal its completion
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

// Keep the timers pomodoro display fixture idle between explicit test steps
func (d *displayDriver) Poll(context.Context, *sdk.Client) error { return nil }

// Assert the expected artwork for the selected display binding
func requireDisplayArtwork(t *testing.T, events []sdk.Event, id, icon string) {
	t.Helper()
	if visualPayload(events, id, "setImage")["image"] != icon {
		t.Fatal("button image missing or stale", id, icon, events)
	}
	feedback := visualPayload(events, id, "setFeedback")
	pixmap, _ := feedback["icon"].(map[string]any)
	if pixmap["value"] != icon {
		t.Fatal("touch feedback and button artwork differ", id, icon, events)
	}
}

// Assert the expected icon state for the selected display binding
func requireDisplayIcon(t *testing.T, events []sdk.Event, id, icon string, state int) {
	t.Helper()
	requireDisplayArtwork(t, events, id, icon)
	if visualPayload(events, id, "setState")["state"] != float64(state) {
		t.Fatal("button state differs", id, state, events)
	}
}

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

// Verify adaptive deadlines do not slide during input and static plugins suspend
func TestAdaptiveDeadlinesDoNotSlideDuringInputAndStaticPluginsSuspend(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Bindings["a"] = binding{Action: "timer.stopwatch"}
	e.State["a"] = &saved{Started: time.Now().UnixMilli()}
	now := time.Now()
	first := e.NextPoll(now)
	if !e.NextPoll(now.Add(200 * time.Millisecond)).Equal(first) {
		t.Fatal("heartbeat slid during events")
	}
	idle := fixtureengine.New(countertally.Definition, filepath.Join(t.TempDir(), "counter.json"), countertally.Factory)
	defer idle.Close()
	idle.Bindings["a"] = binding{Action: "counter.increment"}
	if !idle.NextPoll(now).IsZero() {
		t.Fatal("static binding retained polling")
	}
	speed := fixtureengine.New(internetspeedtest.Definition, filepath.Join(t.TempDir(), "speed.json"), internetspeedtest.Factory)
	defer speed.Close()
	speed.Bindings["a"] = binding{Action: "speed.run"}
	if !speed.NextPoll(now).IsZero() {
		t.Fatal("idle speed display retained polling")
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

// Verify persisted deadline and counter isolation
func TestPersistedDeadlineAndCounterIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	future := time.Now().Add(time.Hour).UnixMilli()
	e.State["a"] = &saved{Value: 4, Deadline: future, Mode: "countdown"}
	e.State["b"] = &saved{Value: 9}
	if err := e.Persist(); err != nil {
		t.Fatal(err)
	}
	restored := New(path)
	if restored.State["a"].Deadline != future || restored.State["b"].Value != 9 {
		t.Fatal("restart lost state")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("state permissions", info, err)
	}

	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("state must be private to its owner", info.Mode())
	}
	if e.state("a", nil) == e.state("b", nil) {
		t.Fatal("isolated contexts merged")
	}
	if e.state("x", map[string]any{"shared_id": "a"}) != e.State["a"] {
		t.Fatal("shared counter binding")
	}
}

// Verify timer persistence is immediate
func TestTimerPersistenceIsImmediate(t *testing.T) {
	timer := New(filepath.Join(t.TempDir(), "timer.json"))
	defer timer.Close()
	timer.State["timer"] = &saved{Deadline: time.Now().Add(time.Minute).UnixMilli()}
	if err := timer.routinePersist(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(timer.Path); err != nil {
		t.Fatal("recovery-critical timer was delayed", err)
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

// Verify hidden timers restore their deadlines
func TestHiddenTimersRestoreTheirDeadlines(t *testing.T) {
	now := time.Now()
	timerPath := filepath.Join(t.TempDir(), "timer.json")
	timer := New(timerPath)
	timer.state("hidden", nil).Deadline = now.Add(-time.Second).UnixMilli()
	timer.State["hidden"].Mode = "countdown"
	if err := timer.routinePersist(); err != nil {
		t.Fatal(err)
	}
	timer.Close()
	restored := New(timerPath)
	defer restored.Close()
	if restored.NextPoll(now).IsZero() {
		t.Fatal("hidden deadline was lost")
	}
	if err := restored.Poll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if restored.State["hidden"].Deadline != 0 || restored.State["hidden"].Color != "Done" {
		t.Fatal("hidden timer did not finish")
	}
	if !restored.NextPoll(now).IsZero() {
		t.Fatal("finished hidden timer retained polling")
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

// Verify timer cache releases retired contexts and close
func TestTimerCacheReleasesRetiredContextsAndClose(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	for i := range 1000 {
		id := fmt.Sprint(i)
		e.Bindings[id] = binding{Action: "timer.stopwatch"}
		_ = e.DisplayView(id, displayStatus{Text: "Ready"})
		e.ClearContext(id)
		delete(e.Bindings, id)
	}
	if len(e.timerViews) != 0 {
		t.Fatal("retired display templates remain")
	}
	e.Bindings["live"] = binding{Action: "timer.countdown"}
	_ = e.DisplayView("live", displayStatus{Text: "Ready"})
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if len(e.timerViews) != 0 {
		t.Fatal("closed engine retained display templates")
	}
}

// Verify timer cached views follow surface theme state and unicode text
func TestTimerCachedViewsFollowSurfaceThemeStateAndUnicodeText(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	for _, surface := range []*sdk.Surface{nil, {Width: 240, Height: 100}, {Width: 48, Height: 48}, {Width: 512, Height: 512}} {
		for _, theme := range []string{"", "light", "dark"} {
			for _, state := range []int{0, 1, 2, 3, -1, 20} {
				for _, text := range []string{"00:59", "Focus\n00:58", "", strings.Repeat("é界", 300)} {
					e.Theme = theme
					e.Bindings["key"] = binding{Action: "timer.pomodoro", Surface: surface}
					status := displayStatus{State: state, Text: text}
					got := e.DisplayView("key", status)

					runes := []rune(text)
					if len(runes) > 500 {
						status.Text = string(runes[:500])
					}
					want := engine.CoordinatedView(e.Engine, "key", status)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("surface/theme/state/text mismatch: %+v %s %d", surface, theme, state)
					}
				}
			}
		}
	}
}

// Verify timer frames and queued messages remain immutable
func TestTimerFramesAndQueuedMessagesRemainImmutable(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Bindings["key"] = binding{Action: "timer.countdown"}
	e.State["key"] = &saved{Deadline: time.Now().Add(time.Hour).UnixMilli(), FocusMillis: 1000}
	first := e.DisplayView("key", displayStatus{Text: "59:59", State: 1})
	e.Last["key"] = first
	queued := sdk.Message("setFeedback", "key", first.Feedback)
	encoded, _ := json.Marshal(first)
	payload := string(queued.Payload)
	e.State["key"].Deadline = time.Now().Add(-time.Second).UnixMilli()
	second := e.DisplayView("key", displayStatus{Text: "00:00", State: 1})
	if second.Feedback["indicator"].(map[string]any)["value"] != 100.0 {
		t.Fatal("progress did not update")
	}
	e.State["key"].Deadline = 0
	paused := e.DisplayView("key", displayStatus{Text: "00:00", State: 1})
	if paused.Feedback["indicator"].(map[string]any)["value"] != nil || second.Feedback["indicator"].(map[string]any)["value"] != 100.0 {
		t.Fatal("pause failed to clear progress or mutated an earlier frame")
	}
	e.Theme = "light"
	e.Bindings["key"] = binding{Action: "timer.pomodoro", Surface: &sdk.Surface{Width: 240, Height: 100}}
	_ = e.DisplayView("key", displayStatus{Text: "Break", State: 3})
	after, _ := json.Marshal(e.Last["key"])
	if string(after) != string(encoded) || string(queued.Payload) != payload {
		t.Fatal("later frame mutated an accepted/queued frame")
	}
}

// Verify timer wire updates only changed progress and text
func TestTimerWireUpdatesOnlyChangedProgressAndText(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Bindings["key"] = binding{Action: "timer.countdown"}
	e.State["key"] = &saved{Deadline: time.Now().Add(time.Hour).UnixMilli(), FocusMillis: 1000}
	steps := []displayStep{
		{Render: func(ctx context.Context, client *sdk.Client) error {
			return e.SendDisplay(ctx, client, "key", displayStatus{Text: "00:02", State: 1})
		}},
		{before: func() { e.State["key"].Deadline = time.Now().Add(-time.Second).UnixMilli() }, Render: func(ctx context.Context, client *sdk.Client) error {
			return e.SendDisplay(ctx, client, "key", displayStatus{Text: "00:02", State: 1})
		}},
		{Render: func(ctx context.Context, client *sdk.Client) error {
			return e.SendDisplay(ctx, client, "key", displayStatus{Text: "00:01", State: 1})
		}},
	}
	frames := driveDisplays(t, e, steps)
	for i, field := range []string{"indicator", "line0"} {
		feedback := visualPayload(frames[i+1], "key", "setFeedback")
		if len(feedback) != 1 || feedback[field] == nil {
			t.Fatalf("unexpected delta: %+v", feedback)
		}
		for _, event := range frames[i+1] {
			if event.Event == "setImage" || event.Event == "setState" || event.Event == "setFeedbackLayout" {
				t.Fatal("unchanged artwork/state/layout resent", event.Event)
			}
		}
	}
}

// Measure timer frames
func BenchmarkTimerFrames(b *testing.B) {
	for _, wide := range []bool{false, true} {
		b.Run(fmt.Sprintf("wide-%t", wide), func(b *testing.B) {
			e := New(filepath.Join(b.TempDir(), "state.json"))
			defer e.Close()
			ids := make([]string, 32)
			for i := range ids {
				ids[i] = fmt.Sprint(i)
				binding := binding{Action: "timer.countdown"}
				if wide {
					binding.Surface = &sdk.Surface{Width: 240, Height: 100}
				}
				e.Bindings[ids[i]] = binding
				e.State[ids[i]] = &saved{Deadline: time.Now().Add(time.Hour).UnixMilli(), FocusMillis: 3600000}
			}
			tick := 0
			deadline := time.Now().Add(time.Hour).UnixMilli()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				status := displayStatus{State: 1, Text: fmt.Sprintf("%02d:%02d", tick/60%60, tick%60)}
				for _, id := range ids {

					e.State[id].Deadline = deadline - int64(tick%3600)*1000
					timerFrameSink = e.DisplayView(id, status)
				}
				tick++
			}
		})
	}
}

var timerFrameSink pluginDisplay

type Engine = Plugin

var Definitions = []engine.Definition{Definition}

// Schedule timers pomodoro fixture work through the shared engine job lifecycle
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}
