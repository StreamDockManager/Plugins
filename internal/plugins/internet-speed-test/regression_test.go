package internetspeedtest

import (
	"context"
	"encoding/json"
	"errors"
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

var displayEvent = testhost.DisplayEvent

// Drive internet speed test fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested internet speed test fixture step and signal its completion
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

// Keep the internet speed test display fixture idle between explicit test steps
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

// Verify deterministic speed history keeps latest twenty and uses owned context
func TestDeterministicSpeedHistoryKeepsLatestTwentyAndUsesOwnedContext(t *testing.T) {
	engine := New(filepath.Join(t.TempDir(), "state.json"))
	runs := 0
	engine.Global = map[string]any{"server_mode": "auto"}
	engine.speed = func(ctx context.Context, server string, progress func(SpeedUpdate)) (SpeedResult, error) {
		runs++
		progress(SpeedUpdate{Action: "run", Text: "fixture progress"})
		return SpeedResult{At: fmt.Sprint(runs), Server: "fixture", LatencyMS: 12, DownloadMbps: 123, UploadMbps: 45}, nil
	}
	steps := []displayStep{{event: displayEvent("willAppear", "speed", "run")}}
	for range 21 {
		steps = append(steps, displayStep{event: displayEvent("keyDown", "speed", "run")}, displayStep{before: func() {
			deadline := time.Now().Add(time.Second)
			for len(engine.Completed) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if len(engine.Completed) == 0 {
				t.Fatal("deterministic speed runner did not complete")
			}
		}})
	}
	all := driveDisplays(t, engine, steps)
	values := engine.state("speed-history", nil).SpeedResults
	if len(values) != 20 || values[0].At != "2" || values[19].At != "21" {
		t.Fatal(values)
	}
	for _, events := range all {
		for _, event := range events {
			if event.Event == "sendToPropertyInspector" && event.Context != "speed" {
				t.Fatal("unowned synthetic job context escaped", event.Context)
			}
		}
	}
	restored := New(engine.Path)
	if len(restored.state("speed-history", nil).SpeedResults) != 20 {
		t.Fatal("history did not survive restart")
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

// Verify speed failure and cancellation preserve other measurements
func TestSpeedFailureAndCancellationPreserveOtherMeasurements(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancel), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			e.state("speed-history", nil).SpeedResults = []SpeedResult{{LatencyMS: 5, DownloadMbps: 90, UploadMbps: 30}}
			emit, finish := speedFixture(t, e)
			stop := displayStep{before: func() { finish(fmt.Errorf("download failed")) }}
			if cancel {
				stop = displayStep{event: displayEvent("keyDown", "cancel", "speed.cancel")}
			}
			all := driveDisplays(t, e, []displayStep{
				{event: displayEvent("willAppear", "run", "speed.run")},
				{event: displayEvent("willAppear", "cancel", "speed.cancel")},
				{event: displayEvent("willAppear", "latency", "speed.latency")},
				{event: displayEvent("willAppear", "download", "speed.download")},
				{event: displayEvent("willAppear", "upload", "speed.upload")},
				{event: displayEvent("keyDown", "run", "speed.run")},
				{before: func() {
					emit(SpeedUpdate{Action: "latency", Text: "12.0 ms", Complete: true})
					emit(SpeedUpdate{Action: "download", Text: "Download · 30%"})
				}},
				stop,
				{before: func() {
					e.progress <- speedProgress{Generation: 0, Update: SpeedUpdate{Action: "upload", Text: "stale upload"}}
				}},
			})
			if cancel {
				requireUpdatedContexts(t, all[7], "run", "download", "cancel")
			} else {
				requireUpdatedContexts(t, all[7], "run", "download")
			}
			requireUpdatedContexts(t, all[8])
			want := "Unavailable\ndownload failed"
			if cancel {
				want = "Cancelled"
			}
			if e.speedStatus("download").Text != want || e.speedStatus("latency").Text != "12.0 ms" || e.speedStatus("upload").Text != "↑ 30.0 Mbps" {
				t.Fatal("failure/cancellation overwrote unrelated measurements", e.speedDisplay)
			}
			if len(e.state("speed-history", nil).SpeedResults) != 1 {
				t.Fatal("failed test changed history")
			}
		})
	}
}

// Verify speed metric settings do not cancel the run
func TestSpeedMetricSettingsDoNotCancelTheRun(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	emit, finish := speedFixture(t, e)
	all := driveDisplays(t, e, []displayStep{
		{event: displayEvent("willAppear", "run", "speed.run")},
		{event: displayEvent("willAppear", "second-run", "speed.run")},
		{event: displayEvent("willAppear", "download", "speed.download")},
		{event: displayEvent("keyDown", "run", "speed.run")},
		{event: displayEvent("keyDown", "second-run", "speed.run")},
		{event: displayEvent("didReceiveSettings", "download", "speed.download")},
		{before: func() { emit(SpeedUpdate{Action: "upload", Text: "Upload · 65%"}) }},
		{before: func() { finish(nil) }},
	})
	if e.speedContext != "run" {
		t.Fatal("duplicate run stole completion context", e.speedContext)
	}
	requireUpdatedContexts(t, all[4], "second-run")
	requireUpdatedContexts(t, all[5])
	for _, events := range all {
		for _, event := range events {
			if event.Event == "sendToPropertyInspector" && event.Context != "run" {
				t.Fatal("result sent to another action", event.Context)
			}
		}
	}
}

// Verify speed phases only update matching actions
func TestSpeedPhasesOnlyUpdateMatchingActions(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint("history=", history), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			if history {
				e.state("speed-history", nil).SpeedResults = []SpeedResult{{LatencyMS: 5, DownloadMbps: 90, UploadMbps: 30}}
			}
			emit, finish := speedFixture(t, e)
			all := driveDisplays(t, e, []displayStep{
				{event: displayEvent("willAppear", "run", "speed.run")},
				{event: displayEvent("willAppear", "cancel", "speed.cancel")},
				{event: displayEvent("willAppear", "latency", "speed.latency")},
				{event: displayEvent("willAppear", "download", "speed.download")},
				{event: displayEvent("willAppear", "touch", "speed.download")},
				{event: displayEvent("willAppear", "upload", "speed.upload")},
				{event: displayEvent("keyDown", "run", "speed.run")},
				{before: func() { emit(SpeedUpdate{Action: "run", Text: "Selecting server · 0%"}) }},
				{before: func() { emit(SpeedUpdate{Action: "latency", Text: "Latency · 10%"}) }},
				{before: func() {
					emit(SpeedUpdate{Action: "latency", Text: "12.0 ms", Complete: true})
					emit(SpeedUpdate{Action: "download", Text: "Download · 30%"})
				}},
				{before: func() {
					emit(SpeedUpdate{Action: "download", Text: "↓ 123.0 Mbps", Complete: true})
					emit(SpeedUpdate{Action: "upload", Text: "Upload · 65%"})
				}},
				{event: displayEvent("willAppear", "late-download", "speed.download")},
				{before: func() {
					emit(SpeedUpdate{Action: "upload", Text: "↑ 45.0 Mbps", Complete: true})
					finish(nil)
				}},
				{},
			})
			requireUpdatedContexts(t, all[6], "run")
			requireUpdatedContexts(t, all[7], "run")
			requireUpdatedContexts(t, all[8], "run", "latency")
			requireUpdatedContexts(t, all[9], "run", "latency", "download", "touch")
			requireUpdatedContexts(t, all[10], "run", "download", "touch", "upload")
			requireUpdatedContexts(t, all[11], "late-download")
			requireUpdatedContexts(t, all[12], "run", "upload")
			requireUpdatedContexts(t, all[13])
			for _, check := range []struct {
				step     int
				id, text string
			}{
				{8, "latency", "Latency · 10%"},
				{9, "latency", "12.0 ms"},
				{9, "download", "Download · 30%"},
				{10, "download", "↓ 123.0 Mbps"},
				{10, "upload", "Upload · 65%"},
				{11, "late-download", "↓ 123.0 Mbps"},
				{12, "upload", "↑ 45.0 Mbps"},
			} {
				if visualPayload(all[check.step], check.id, "setTitle")["title"] != check.text {
					t.Errorf("step %d %s did not display %q", check.step, check.id, check.text)
				}
			}
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

// Create a speed-test engine with deterministic fixture state
func speedFixture(t *testing.T, e *Engine) (func(SpeedUpdate), func(error)) {
	t.Helper()
	updates := make(chan SpeedUpdate)
	applied := make(chan struct{}, 1)
	finished := make(chan error)
	e.Global = map[string]any{"server_mode": "auto"}
	e.speed = func(ctx context.Context, _ string, progress func(SpeedUpdate)) (SpeedResult, error) {
		for {
			select {
			case update := <-updates:
				progress(update)
				applied <- struct{}{}
			case err := <-finished:
				return SpeedResult{LatencyMS: 12, DownloadMbps: 123, UploadMbps: 45}, err
			case <-ctx.Done():
				return SpeedResult{}, ctx.Err()
			}
		}
	}
	emit := func(update SpeedUpdate) {
		select {
		case updates <- update:
		case <-time.After(time.Second):
			t.Error("speed runner did not accept update")
			return
		}
		select {
		case <-applied:
		case <-time.After(time.Second):
			t.Error("speed runner did not report update")
		}
	}
	finish := func(err error) {
		select {
		case finished <- err:
		case <-time.After(time.Second):
			t.Error("speed runner did not finish")
			return
		}
		deadline := time.Now().Add(time.Second)
		for len(e.Completed) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(e.Completed) == 0 {
			t.Error("speed result was not queued")
		}
	}
	return emit, finish
}

type Engine = Plugin

var Definitions = []engine.Definition{Definition}

// Verify final queued progress survives failure completion
func TestFinalQueuedProgressSurvivesFailureCompletion(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.speedDisplay = map[string]string{}
	e.progress = make(chan speedProgress, 1)
	e.Jobs["speed-session"] = backgroundJob{Generation: 7, Cancel: func() {}}
	e.progress <- speedProgress{Generation: 7, Update: SpeedUpdate{Action: "latency", Text: "12 ms", Complete: true}}
	if err := e.ApplyJobResult(context.Background(), nil, jobResult{ID: "speed-session", Generation: 7, Err: errors.New("download failed")}); err != nil {
		t.Fatal(err)
	}
	if e.speedDisplay["latency"] != "12 ms" {
		t.Fatal("completion discarded queued successful phase", e.speedDisplay)
	}
}

// Verify progress uses current job identity
func TestProgressUsesCurrentJobIdentity(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.speedDisplay = map[string]string{}
	e.progress = make(chan speedProgress, 3)
	e.Jobs["speed-session"] = backgroundJob{Generation: 8, Cancel: func() {}}
	e.progress <- speedProgress{Generation: 7, Update: SpeedUpdate{Action: "latency", Text: "stale", Complete: true}}
	e.drainSpeedProgress()
	if e.speedDisplay["latency"] != "" {
		t.Fatal("stale progress accepted")
	}
	e.progress <- speedProgress{Generation: 8, Update: SpeedUpdate{Action: "latency", Text: "current", Complete: true}}
	e.drainSpeedProgress()
	if e.speedDisplay["latency"] != "current" {
		t.Fatal("current progress rejected")
	}
	e.CancelJob("speed-session")
	e.progress <- speedProgress{Generation: 8, Update: SpeedUpdate{Action: "latency", Text: "late", Complete: true}}
	e.drainSpeedProgress()
	if e.speedDisplay["latency"] != "current" {
		t.Fatal("cancelled progress accepted")
	}
}
