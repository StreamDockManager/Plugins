package systemmonitor

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

// Verify telemetry only updates changed measurements
func TestTelemetryOnlyUpdatesChangedMeasurements(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	sample := func(cpu, memory float64) sdk.TelemetrySnapshot {
		return sdk.TelemetrySnapshot{
			CPU:    sdk.Measurement{Value: &cpu, ObservedAt: uint64(time.Now().UnixMilli()), MaxAgeMS: 10000},
			Memory: sdk.Measurement{Value: &memory, ObservedAt: uint64(time.Now().UnixMilli()), MaxAgeMS: 10000},
		}
	}
	initial := sample(30, 50)
	e.sharedTelemetry = &initial
	all := driveDisplays(t, e, []displayStep{
		{event: displayEvent("willAppear", "cpu", "system.cpu")},
		{event: displayEvent("willAppear", "touch", "system.cpu")},
		{event: displayEvent("willAppear", "memory", "system.memory")},
		{event: sdk.Message("didReceiveTelemetry", "", sample(30, 50))},
		{event: sdk.Message("didReceiveTelemetry", "", sample(40, 50))},
		{event: sdk.Message("didReceiveTelemetry", "", sdk.TelemetrySnapshot{Memory: initial.Memory})},
	})
	requireUpdatedContexts(t, all[3])
	requireUpdatedContexts(t, all[4], "cpu", "touch")
	requireUpdatedContexts(t, all[5], "cpu", "touch")
	if visualPayload(all[5], "cpu", "setTitle")["title"] != "CPU\nUnavailable" {
		t.Fatal("an unavailable sample did not replace the old value")
	}
}

var displayEvent = testhost.DisplayEvent

// Drive system monitor fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested system monitor fixture step and signal its completion
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

// Keep the system monitor display fixture idle between explicit test steps
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

// Verify failed telemetry render keeps due expiry armed
func TestFailedTelemetryRenderKeepsDueExpiryArmed(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.UnixMilli(1700000000000)
	value := 42.0
	e.Bindings["cpu"] = binding{Action: "system-monitor.cpu"}
	e.sharedTelemetry = &sdk.TelemetrySnapshot{CPU: sdk.Measurement{Value: &value, ObservedAt: uint64(now.UnixMilli()), MaxAgeMS: 2500}}
	expiry := e.NextPoll(now)

	if err := e.renderSharedTelemetry(context.Background(), &sdk.Client{}, expiry); err == nil {
		t.Fatal("display unexpectedly accepted by a full client")
	}
	if next := e.NextPoll(expiry.Add(time.Millisecond)); !next.Equal(expiry) {
		t.Fatal("failed render consumed the due expiry", next)
	}
}

// Verify shared telemetry expiry ignores unavailable fields and preserves pending writes
func TestSharedTelemetryExpiryIgnoresUnavailableFieldsAndPreservesPendingWrites(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.UnixMilli(1700000000000)
	value := 42.0
	errorText := "Unavailable"
	e.Bindings["cpu"] = binding{Action: "system-monitor.cpu"}
	e.sharedTelemetry = &sdk.TelemetrySnapshot{CPU: sdk.Measurement{Value: &value, ObservedAt: uint64(now.UnixMilli()), MaxAgeMS: 2500, Error: &errorText}}
	if next := e.NextPoll(now); !next.IsZero() {
		t.Fatal("unavailable telemetry retained polling", next)
	}
	e.sharedTelemetry.CPU.Error = nil
	e.PendingWrite = now.Add(250 * time.Millisecond)
	if next := e.NextPoll(now); !next.Equal(e.PendingWrite) {
		t.Fatal("telemetry expiry delayed a pending state write", next)
	}
	e.PendingWrite = time.Time{}
}

// Verify shared telemetry schedules freshness expiry without another snapshot
func TestSharedTelemetrySchedulesFreshnessExpiryWithoutAnotherSnapshot(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.UnixMilli(1700000000000)
	cpu, disk := 42.0, 60.0
	diskPath := t.TempDir()
	e.Bindings["cpu"] = binding{Action: "system-monitor.cpu"}
	e.Bindings["disk"] = binding{Action: "system-monitor.disk", Settings: map[string]any{"path": diskPath}}
	e.sharedTelemetry = &sdk.TelemetrySnapshot{
		CPU:   sdk.Measurement{Value: &cpu, ObservedAt: uint64(now.UnixMilli()), MaxAgeMS: 2500},
		Disks: map[string]sdk.Measurement{diskPath: {Value: &disk, ObservedAt: uint64(now.UnixMilli()), MaxAgeMS: 11000}},
	}
	cpuExpiry, diskExpiry := now.Add(2501*time.Millisecond), now.Add(11001*time.Millisecond)
	if next := e.NextPoll(now); !next.Equal(cpuExpiry) {
		t.Fatalf("fresh telemetry needs an expiry wake: got %v, want %v", next, cpuExpiry)
	}
	if next := e.NextPoll(now.Add(time.Second)); !next.Equal(cpuExpiry) {
		t.Fatal("freshness deadline slid with input")
	}
	if status := e.sharedSystemStatusAt(e.Bindings["cpu"], cpuExpiry.Add(-time.Millisecond)); status.Text != "CPU 42.0%" || status.Unavailable {
		t.Fatal("CPU became stale before its age limit", status)
	}
	if status := e.sharedSystemStatusAt(e.Bindings["cpu"], cpuExpiry); status.Text != "CPU\nStale" || !status.Unavailable {
		t.Fatal("expired telemetry still looks fresh", status)
	}
	all := driveDisplays(t, e, []displayStep{
		{Render: func(ctx context.Context, c *sdk.Client) error {
			for _, at := range []time.Time{cpuExpiry, cpuExpiry.Add(time.Millisecond), diskExpiry} {
				if next := e.NextPoll(at); !next.Equal(cpuExpiry) {
					t.Error("rescheduling consumed an expiry before rendering stale values", next)
				}
			}
			if err := e.renderSharedTelemetry(ctx, c, cpuExpiry); err != nil {
				return err
			}
			if next := e.NextPoll(cpuExpiry); !next.Equal(diskExpiry) {
				t.Error("completed CPU expiry render did not advance to disk expiry", next)
			}
			return nil
		}},
		{Render: func(ctx context.Context, c *sdk.Client) error {
			if next := e.NextPoll(diskExpiry); !next.Equal(diskExpiry) {
				t.Error("last fresh field lost its due expiry before rendering", next)
			}
			if err := e.renderSharedTelemetry(ctx, c, diskExpiry); err != nil {
				return err
			}
			if next := e.NextPoll(diskExpiry); !next.IsZero() {
				t.Error("all-stale telemetry retained polling after rendering", next)
			}
			return nil
		}},
		{Render: func(ctx context.Context, c *sdk.Client) error {
			e.sharedTelemetry.CPU.ObservedAt = uint64(diskExpiry.UnixMilli())
			if err := e.renderSharedTelemetry(ctx, c, diskExpiry); err != nil {
				return err
			}
			if next := e.NextPoll(diskExpiry); !next.Equal(diskExpiry.Add(2501 * time.Millisecond)) {
				t.Error("new snapshot did not schedule a fresh expiry", next)
			}
			clear(e.Bindings)
			if next := e.NextPoll(diskExpiry); !next.IsZero() {
				t.Error("unused telemetry retained polling", next)
			}
			return nil
		}},
	})
	if visualPayload(all[0], "cpu", "setTitle")["title"] != "CPU\nStale" || visualPayload(all[1], "disk", "setTitle")["title"] != "Disk\nStale" || visualPayload(all[2], "cpu", "setTitle")["title"] != "CPU 42.0%" {
		t.Fatal("expiry and recovery were not delivered to the visible controls")
	}
}

type Engine = Plugin

var Definitions = []engine.Definition{Definition}
