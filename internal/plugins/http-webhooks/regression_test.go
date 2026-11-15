package httpwebhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"io"
	"maps"
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
			if len(all[2]) < 1 || len(all[2]) > 2 || all[2][0].Event != "setTitle" {
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

// Drive HTTP webhook fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested HTTP webhook fixture step and signal its completion
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

// Keep the HTTP webhook display fixture idle between explicit test steps
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

// Verify cancelled generation cannot overwrite new settings
func TestCancelledGenerationCannotOverwriteNewSettings(t *testing.T) {
	engine := New(filepath.Join(t.TempDir(), "state.json"))
	ctx := t.Context()
	started := make(chan struct{})
	finished := make(chan struct{})
	engine.startJob(ctx, "binding", func(job context.Context) (string, any, error) {
		close(started)
		<-job.Done()
		close(finished)
		return "stale", nil, nil
	})
	<-started
	old := engine.Jobs["binding"].Generation
	engine.CancelJob("binding")
	<-finished
	if _, exists := engine.Jobs["binding"]; exists {
		t.Fatal("cancelled job remains")
	}
	if !engine.startJob(ctx, "binding", func(job context.Context) (string, any, error) {
		<-job.Done()
		return "replacement", nil, nil
	}) {
		t.Fatal("replacement admission failed")
	}
	current := engine.Jobs["binding"].Generation
	if current <= old {
		t.Fatal("replacement reused the cancelled generation")
	}
	engine.Completed <- jobResult{ID: "binding", Generation: old, Text: "stale"}
	if err := engine.DrainJobs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if engine.ResultsText["binding"] == "stale" || engine.Jobs["binding"].Generation != current {
		t.Fatal("cancelled result applied or removed the replacement")
	}
	_ = engine.Close()
}

// Verify HTTP bounded requests and environment
func TestHTTPBoundedRequestsAndEnvironment(t *testing.T) {
	t.Setenv("SDM_TEST_SECRET", "fixture-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
		case "/slow":
			<-r.Context().Done()
		default:
			if r.Header.Get("Authorization") != "Bearer fixture-secret" {
				t.Error("environment credential not expanded")
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"n":90071992547409931234,"nullable":null}`)
		}
	}))
	defer server.Close()
	settings := map[string]any{"url": server.URL, "headers": map[string]any{"Authorization": "Bearer ${SDM_TEST_SECRET}"}, "fields": map[string]any{"value": "n", "null": "nullable", "missing": "unknown"}}
	result, err := RequestHTTP(context.Background(), settings)
	if err != nil || result.Status != 201 || resultText(result.Fields["value"]) != "90071992547409931234" || len(result.Missing) != 1 {
		t.Fatal(result, err)
	}
	settings["url"] = server.URL + "/large"
	if _, err = RequestHTTP(context.Background(), settings); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatal("response limit not enforced", err)
	}
	settings["url"] = server.URL + "/slow"
	settings["timeout"] = 0.01
	if _, err = RequestHTTP(context.Background(), settings); err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("timeout diagnostics", err)
	}
}

// Verify HTTP paths precision and null
func TestHTTPPathsPrecisionAndNull(t *testing.T) {
	var root any
	decoder := json.NewDecoder(strings.NewReader(`{"data":{"user":{"name":"Ada"},"items":[{"name":"first"}],"a.b":null,"number":90071992547409931234},"":true}`))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"data.user.name": "\"Ada\"", "data.items[0].name": "\"first\"", "data.items.0.name": "\"first\"", `data.a\.b`: "null", `data["a.b"]`: "null", `data.'a.b'`: "null", "data.number": "90071992547409931234", `[""]`: "true"} {
		value, found, err := ExtractPath(root, path)
		if err != nil || !found || resultText(value) != want {
			t.Errorf("%q = %#v %v %v", path, value, found, err)
		}
	}
	if value, found, err := ExtractPath(root, "data.missing"); err != nil || found || value != nil {
		t.Fatal("missing conflated with null")
	}
	if _, found, err := ExtractPath(root, ""); !found || err != nil {
		t.Fatal("empty root path failed")
	}
	for _, path := range []string{".", "data..user", "data.items[-1]", "data[", "data[0", "data.", `data["x]`, `data\`} {
		if _, err := ParsePath(path); err == nil {
			t.Errorf("accepted malformed %q", path)
		}
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

// Verify canceled queued job wakes the loop and releases its slot
func TestCanceledQueuedJobWakesTheLoopAndReleasesItsSlot(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	defer close(release)
	for i := range 4 {
		e.startJob(context.Background(), fmt.Sprint(i), func(context.Context) (string, any, error) { started <- struct{}{}; <-release; return "", nil, nil })
	}
	for range 4 {
		<-started
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.startJob(ctx, "queued", func(context.Context) (string, any, error) {
		t.Error("canceled queued work executed")
		return "", nil, nil
	})
	cancel()
	select {
	case <-e.WakeChannel:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not wake the loop")
	}

	if err := e.DrainJobs(context.Background(), &sdk.Client{}); err != nil {
		t.Fatal(err)
	}
	if _, busy := e.Jobs["queued"]; busy {
		t.Fatal("canceled queued job retained a slot")
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

// Verify automatic polling wire feedback keeps stale values retry time and recovers
func TestAutomaticPollingWireFeedbackKeepsStaleValuesRetryTimeAndRecovers(t *testing.T) {
	var stage atomic.Int32
	var revalidated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"wire"`)
		if stage.Load() == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(503)
			return
		}
		if r.Header.Get("If-None-Match") == `"wire"` {
			revalidated.Add(1)
			w.WriteHeader(304)
			return
		}
		_, _ = w.Write([]byte(`{"value":42}`))
	}))
	defer server.Close()
	e := New(filepath.Join(t.TempDir(), "state.json"))
	settings := map[string]any{"url": server.URL, "update_interval_ms": float64(1000), "field": "value", "fields": map[string]any{"value": "value"}}
	var steps []displayStep
	for _, a := range []string{"field", "status", "duration"} {
		event := displayEvent("willAppear", a, "http."+a)
		event.Payload, _ = json.Marshal(map[string]any{"settings": settings})
		steps = append(steps, displayStep{event: event})
	}
	poll := func(ctx context.Context, c *sdk.Client) error {
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
		if len(e.Jobs) != 0 {
			return errors.New("HTTP wire jobs did not finish")
		}
		return nil
	}
	steps = append(steps, displayStep{Render: poll})
	kick := func(value int32) {
		stage.Store(value)
		for id, state := range e.polling {
			state.Next = time.Time{}
			e.polling[id] = state
		}
	}
	steps = append(steps, displayStep{before: func() {
		var earliest time.Time
		for id, state := range e.polling {
			if state.Next.Before(time.Now()) {
				t.Fatal("poll deadline did not follow completion", id, state)
			}
			if earliest.IsZero() || state.Next.Before(earliest) {
				earliest = state.Next
			}
		}
		if !e.NextPoll(time.Now()).Equal(earliest) {
			t.Fatal("owner did not schedule earliest completion deadline")
		}
		kick(1)
	}, Render: poll})
	steps = append(steps, displayStep{before: func() {
		if len(e.Results) != 3 || e.Results["field"].(HTTPResult).Fields["value"] == nil {
			t.Fatal("failure discarded fields")
		}
		for _, state := range e.polling {
			if !state.Stale || state.Failures != 1 || time.Until(state.Next) < 50*time.Second {
				t.Fatal("Retry-After was not scheduled", state)
			}
		}
		kick(2)
	}, Render: poll})
	steps = append(steps, displayStep{Render: func(ctx context.Context, c *sdk.Client) error {
		for _, state := range e.polling {
			if state.Stale || state.Failures != 0 {
				t.Fatal("revalidation did not reset failure state")
			}
		}
		return nil
	}})
	all := driveDisplays(t, e, steps)
	for _, a := range []string{"field", "status", "duration"} {
		title, _ := visualPayload(all[4], a, "setTitle")["title"].(string)
		if !strings.HasPrefix(title, "Stale\n") || !strings.Contains(title, "\nRetry ") {
			t.Fatal("stale title lost", a, title)
		}
		feedback := visualPayload(all[4], a, "setFeedback")
		retry, _ := feedback["line2"].(string)
		if field, ok := feedback["line2"].(map[string]any); ok {
			retry, _ = field["value"].(string)
		}
		if !strings.HasPrefix(retry, "Retry ") || strings.HasSuffix(retry, ">") {
			t.Fatal("retry time clipped from feedback", a, feedback)
		}
		diagnostic := visualPayload(all[4], a, "sendToPropertyInspector")
		nextRetry, _ := diagnostic["next_retry"].(string)
		lastValue, _ := diagnostic["last_successful_value"].(string)
		if diagnostic["stale"] != true || nextRetry == "" || lastValue == "" {
			t.Fatal("exact retry diagnostics missing", diagnostic)
		}
	}
	fieldFeedback := map[string]any{}
	for _, step := range all[:5] {
		for _, event := range step {
			if event.Event == "setFeedback" && event.Context == "field" {
				var delta map[string]any
				_ = json.Unmarshal(event.Payload, &delta)
				maps.Copy(fieldFeedback, delta)
			}
		}
	}
	field, _ := fieldFeedback["line0"].(map[string]any)
	if field["value"] != "42" || revalidated.Load() < 3 {
		t.Fatal("field or conditional recovery missing", all[4], revalidated.Load())
	}
}

// Verify explicit write completes once without automatic replay
func TestExplicitWriteCompletesOnceWithoutAutomaticReplay(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
	}))
	defer server.Close()
	e := New(filepath.Join(t.TempDir(), "state.json"))
	event := displayEvent("willAppear", "write", "http.request")
	event.Payload, _ = json.Marshal(map[string]any{"settings": map[string]any{"url": server.URL, "method": "POST", "update_interval_ms": float64(1000)}})
	press := event
	press.Event = "keyDown"
	driveDisplays(t, e, []displayStep{{event: event}, {event: press}, {Render: func(ctx context.Context, c *sdk.Client) error {
		deadline := time.Now().Add(2 * time.Second)
		for len(e.Jobs) > 0 && time.Now().Before(deadline) {
			if err := e.DrainJobs(ctx, c); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		if !e.NextPoll(time.Now()).IsZero() {
			t.Fatal("write acquired an automatic deadline")
		}
		for range 3 {
			if err := e.Poll(ctx, c); err != nil {
				return err
			}
		}
		return nil
	}}})
	if writes.Load() != 1 {
		t.Fatal("write was replayed", writes.Load())
	}
}

// Verify polling deadlines are completion based preserve stale data and reset on revalidation
func TestPollingDeadlinesAreCompletionBasedPreserveStaleDataAndResetOnRevalidation(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.Now()
	job := pollingJob{Interval: 10 * time.Second}
	text, retain := e.completePolling("one", job, jobResult{Text: "HTTP 200\n42", Value: HTTPResult{Status: 200}}, now, 0)
	if retain || text != "HTTP 200\n42" || !e.polling["one"].Next.Equal(now.Add(10*time.Second)) {
		t.Fatal(text, retain, e.polling)
	}
	if state := e.polling["one"]; !state.Available || !state.ObservedAt.Equal(now) || state.LastValue != "42" {
		t.Fatal("successful poll did not preserve its observation", state)
	}
	retry := now.Add(2 * time.Minute)
	text, retain = e.completePolling("one", job, jobResult{Value: HTTPResult{Status: 503, RetryAfter: retry}}, now.Add(time.Second), 1)
	if !retain || !strings.Contains(text, "Stale\n42") || !strings.Contains(text, "Retry ") || !e.polling["one"].Next.Equal(retry) || e.polling["one"].Failures != 1 {
		t.Fatal(text, e.polling)
	}
	if state := e.polling["one"]; !state.ObservedAt.Equal(now) || state.LastValue != "42" {
		t.Fatal("failed poll replaced last-good observation", state)
	}
	feedback := e.annotatePolling("one", jobResult{Value: HTTPResult{Status: 503}, Err: errors.New("offline")}).(HTTPResult)
	if !feedback.Stale || feedback.LastValue != "42" || feedback.NextRetry != retry.UTC().Format(time.RFC3339) || feedback.Failure != "offline" {
		t.Fatal("poll feedback lost last-good data or retry", feedback)
	}
	e.completePolling("one", job, jobResult{Value: HTTPResult{Status: 304, Revalidated: true}, Text: "HTTP 304\n42"}, now.Add(3*time.Minute), 0)
	if e.polling["one"].Failures != 0 || e.polling["one"].Stale || !e.polling["one"].Next.Equal(now.Add(3*time.Minute+10*time.Second)) {
		t.Fatal(e.polling)
	}
	e.Results["one"] = HTTPResult{Status: 200, Fields: map[string]any{"value": 42}}
	e.Jobs["one"] = backgroundJob{Poll: &job}
	if err := e.applyJobResult(context.Background(), nil, jobResult{ID: "one", Err: errors.New("offline")}); err != nil {
		t.Fatal(err)
	}
	if result, ok := e.Results["one"].(HTTPResult); !ok || result.Fields["value"] != 42 || !e.asyncStatus("one").Unavailable {
		t.Fatal("last successful fields were discarded", e.Results, e.ResultsText)
	}
	e.ClearContext("one")
	if len(e.polling) != 0 || len(e.Results) != 0 {
		t.Fatal("departing binding retained poll state")
	}
}

// Verify polling only replays safe methods and bounds intervals
func TestPollingOnlyReplaysSafeMethodsAndBoundsIntervals(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if pollingInterval("http-webhooks", "request", map[string]any{"method": method, "update_interval_ms": float64(1000)}) != 0 {
			t.Fatal("write scheduled", method)
		}
	}
	for _, seconds := range []float64{-1, 0, 0.5, 86401} {
		if pollingInterval("http-webhooks", "request", map[string]any{"interval": seconds}) != 0 {
			t.Fatal("invalid interval scheduled", seconds)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		if pollingInterval("http-webhooks", "request", map[string]any{"method": method, "update_interval_ms": float64(10000)}) != 10*time.Second {
			t.Fatal(method)
		}
	}
	if pollingInterval("http-webhooks", "cancel", map[string]any{"update_interval_ms": float64(10000)}) != 0 || pollingInterval("service-monitor", "refresh", nil) != 0 {
		t.Fatal("non-polling action scheduled")
	}
}

// Verify retry after forms and bounded backoff
func TestRetryAfterFormsAndBoundedBackoff(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for value, want := range map[string]time.Duration{"120": 120 * time.Second, "0": 0, now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second, now.Add(-time.Hour).Format(http.TimeFormat): 0} {
		if got := parseRetryAfter(value, now); !got.Equal(now.Add(want)) {
			t.Fatal(value, got, want)
		}
	}
	// The pinned SDK saturates nanoseconds; coordinated SDK development uses
	// the portable whole-second cap. Exact cross-language parity lives in SDK
	// fixtures; this consumer supports both without changing its dependency pin
	got := parseRetryAfter("999999999999999999999999999", now)
	wholeSecondCap := maximumRetryAfter / time.Second * time.Second
	if !got.Equal(now.Add(maximumRetryAfter)) && !got.Equal(now.Add(wholeSecondCap)) {
		t.Fatal("unbounded Retry-After", got)
	}
	for _, value := range []string{"", "-1", "1.5", "nonsense", "NaN"} {
		if !parseRetryAfter(value, now).IsZero() {
			t.Fatal("invalid Retry-After", value)
		}
	}
	if retryDelay(time.Second, 1, 0) != 2*time.Second || retryDelay(time.Second, 2, 1) != 5*time.Second || retryDelay(time.Second, 16, 1) != maximumPollBackoff || retryDelay(time.Hour, 16, 1) != time.Hour {
		t.Fatal("backoff bounds or jitter")
	}
}

// Verify successful and revalidated responses honor retry after without retaining failures
func TestSuccessfulAndRevalidatedResponsesHonorRetryAfterWithoutRetainingFailures(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.Now()
	for _, status := range []int{200, 202, 304} {
		e.polling["paced"] = pollingState{Failures: 5, Stale: true}
		e.completePolling("paced", pollingJob{Interval: time.Second}, jobResult{Value: HTTPResult{Status: status, Revalidated: status == 304, RetryAfter: now.Add(48 * time.Hour)}}, now, 0)
		state := e.polling["paced"]
		if state.Failures != 0 || state.Stale || !state.Next.Equal(now.Add(48*time.Hour)) {
			t.Fatal("successful Retry-After ignored", status, state)
		}
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

// Apply a completed HTTP webhook fixture job through its normal result path
func (e *Plugin) applyJobResult(ctx context.Context, c *sdk.Client, r jobResult) error {
	return e.ApplyJobResult(ctx, c, r)
}

var ParsePath = httpclient.ParsePath
var PathSuggestions = httpclient.PathSuggestions

var ExtractPath = httpclient.ExtractPath

// Verify binding settings preserve other polling and validators
func TestBindingSettingsPreserveOtherPollingAndValidators(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	deadline := time.Now().Add(10 * time.Minute)
	e.polling["limited"] = pollingState{Next: deadline, Failures: 1, LastValue: "previous value", Stale: true}
	e.ResultsText["limited"] = "Stale\nprevious value"
	e.polling["edited"] = pollingState{Next: deadline}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			if r.Header.Get("If-None-Match") != `"fixture"` {
				t.Error("unrelated settings discarded cached validator")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"value":42}`)
	}))
	defer server.Close()
	s := map[string]any{"url": server.URL, "field": "value"}
	if _, err := requestHTTP(context.Background(), s, httpRequestOptions{Cache: e.httpCache, Automatic: true}); err != nil {
		t.Fatal(err)
	}
	e.settingsFeature("edited")
	if got := e.polling["limited"]; got.Next != deadline || got.Failures != 1 || !got.Stale || got.LastValue != "previous value" {
		t.Fatalf("unrelated poll reset: %+v", got)
	}
	if _, ok := e.polling["edited"]; ok {
		t.Fatal("changed binding retained polling deadline")
	}
	if result, err := requestHTTP(context.Background(), s, httpRequestOptions{Cache: e.httpCache, Automatic: true}); err != nil || !result.Revalidated {
		t.Fatalf("shared request no longer revalidates: %+v %v", result, err)
	}
	e.settingsFeature("")
	if len(e.polling) != 0 || len(e.ResultsText) != 0 {
		t.Fatal("global settings did not reset polling and display")
	}
}

// Verify explicit off loads safe reads once and allows manual refresh
func TestExplicitOffLoadsSafeReadsOnceAndAllowsManualRefresh(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(`{"value":42}`)) }))
	defer server.Close()
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	event := displayEvent("willAppear", "off", "http.field")
	event.Payload, _ = json.Marshal(map[string]any{"settings": map[string]any{"url": server.URL, "field": "value", "update_interval_ms": 0}})
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

// Verify empty polling display remains unavailable on failure
func TestEmptyPollingDisplayRemainsUnavailableOnFailure(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	now := time.Now()
	job := pollingJob{Interval: time.Second}
	e.completePolling("empty", job, jobResult{Text: "HTTP 200\n", Value: HTTPResult{Status: 200}}, now, 0)
	text, retain := e.completePolling("empty", job, jobResult{Err: errors.New("offline")}, now.Add(time.Second), 0)
	if retain || !strings.HasPrefix(text, "Unavailable\n") || e.polling["empty"].Stale {
		t.Fatal("empty display became a stale fallback", text, e.polling["empty"])
	}
}
