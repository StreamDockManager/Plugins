package microphonecontrols

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

// Verify mute icons follow presses external changes and reconnects
func TestMuteIconsFollowPressesExternalChangesAndReconnects(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	mic := &displayMicrophone{}
	e.mic = &microphone.Controller{Adapter: mic}
	all := driveDisplays(t, e, []displayStep{
		{event: displayEvent("willAppear", "key", "mic.toggle")},
		{event: displayEvent("willAppear", "touch", "mic.toggle")},
		{event: displayEvent("keyDown", "key", "mic.toggle")},
		{event: displayEvent("keyUp", "key", "mic.toggle")},
		{before: func() { mic.muted = false }},
		{before: func() { mic.failed = true }},
		{before: func() { mic.failed = false }},
		{event: displayEvent("touchTap", "touch", "mic.toggle")},
		{event: displayEvent("willDisappear", "key", "mic.toggle")},
		{event: displayEvent("willAppear", "key", "mic.toggle")},
	})
	requireDisplayIcon(t, all[0], "key", "icons/microphone.svg", 0)
	requireDisplayIcon(t, all[1], "touch", "icons/microphone.svg", 0)
	if visualPayload(all[0], "key", "setFeedbackLayout")["layout"] != "display/key.json" || visualPayload(all[1], "touch", "setFeedbackLayout")["layout"] != "display/wide.json" {
		t.Fatal("incorrect surface layout")
	}
	requireDisplayIcon(t, all[2], "key", "icons/microphone-off.svg", 1)
	if len(all[3]) != 0 || len(all[8]) != 0 || mic.changes != 2 {
		t.Fatal("release, disappearance or polling changed the microphone", all[3], mic.changes)
	}
	requireDisplayIcon(t, all[4], "key", "icons/microphone.svg", 0)
	for _, id := range []string{"key", "touch"} {
		requireDisplayArtwork(t, all[5], id, "icons/unavailable.svg")
		requireDisplayArtwork(t, all[6], id, "icons/microphone.svg")
		if visualPayload(all[5], id, "setState") != nil || visualPayload(all[6], id, "setState") != nil {
			t.Fatal("unchanged state was resent", id)
		}
	}
	requireDisplayIcon(t, all[7], "touch", "icons/microphone-off.svg", 1)
	requireDisplayIcon(t, all[9], "key", "icons/microphone-off.svg", 1)
	field, _ := visualPayload(all[2], "key", "setFeedback")["line0"].(map[string]any)
	if field["value"] != "Muted" {
		t.Fatal("a long device name hid the mute state")
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

// Drive microphone controls fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Return the fake default microphone identifier
func (m *displayMicrophone) Default(context.Context) (string, error) { return "mic", nil }

// Return the fake microphone device inventory
func (m *displayMicrophone) Devices(ctx context.Context) ([]microphone.Device, error) {
	d, err := m.Read(ctx, "mic")
	return []microphone.Device{d}, err
}

// Execute the requested microphone controls fixture step and signal its completion
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

// Keep the microphone controls display fixture idle between explicit test steps
func (d *displayDriver) Poll(context.Context, *sdk.Client) error { return nil }

// Read the injected microphone state or configured failure
func (m *displayMicrophone) Read(context.Context, string) (microphone.Device, error) {
	if m.failed {
		return microphone.Device{}, fmt.Errorf("device unplugged")
	}
	return microphone.Device{ID: "mic", Name: "Long named desk microphone", Muted: m.muted, Mode: "hardware mute", Gain: .5}, nil
}

// Record a mute change in the fake microphone adapter
func (m *displayMicrophone) SetMute(_ context.Context, _ string, muted bool) error {
	m.muted, m.changes = muted, m.changes+1
	return nil
}

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

type displayMicrophone struct {
	muted, failed bool
	changes       int
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

// Verify microphone input edges
func TestMicrophoneInputEdges(t *testing.T) {
	for _, name := range []string{"keyUp", "dialUp", "dialRotate", "willAppear", "willDisappear"} {
		if active(sdk.Event{Event: name}) {
			t.Fatal("release/rotation activated microphone", name)
		}
	}
	for _, name := range []string{"keyDown", "dialDown", "touchTap"} {
		if !active(sdk.Event{Event: name, Payload: json.RawMessage(`{}`)}) {
			t.Fatal(name)
		}
	}
	_ = context.Background()
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

// Verify official plugin display lifecycle matrix
func TestOfficialPluginDisplayLifecycleMatrix(t *testing.T) {
	for _, definition := range Definitions {
		for _, count := range []int{1, 32} {
			t.Run(fmt.Sprintf("%s/%d", definition.Slug, count), func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
				e.readStatus = func(context.Context, statusRequest) probeStatus {
					return probeStatus{Status: displayStatus{Text: "Ready"}}
				}
				var steps []displayStep
				for i := range count {
					steps = append(steps, displayStep{event: displayEvent("willAppear", fmt.Sprint(i), definition.Actions[0])})
				}
				for range 3 {
					steps = append(steps, displayStep{Render: func(ctx context.Context, c *sdk.Client) error { return e.Poll(ctx, c) }})
				}

				steps = append(steps, displayStep{Render: func(ctx context.Context, c *sdk.Client) error {
					e.StartJob(ctx, "fixture-activation", func(context.Context) (string, any, error) { return "Finished", nil, nil }, e.applyResult)
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

type Engine = Plugin

var Definitions = []engine.Definition{Definition}
