package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// Drive weather fixture steps and collect their emitted display events
func driveDisplays(t *testing.T, e *Engine, steps []displayStep) [][]sdk.Event {
	t.Helper()
	return testhost.DriveDisplays(t, len(steps), &displayDriver{Engine: e, steps: steps})
}

// Execute the requested weather fixture step and signal its completion
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

// Keep the weather display fixture idle between explicit test steps
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

// Verify weather cancellation does not return cached success
func TestWeatherCancellationDoesNotReturnCachedSuccess(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.weatherText(ctx, "conditions", weatherSettings()); err != context.Canceled {
		t.Fatal("cancellation returned a success", err)
	}
}

// Verify weather consent types locations and shared response cache
func TestWeatherConsentTypesLocationsAndSharedResponseCache(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	var calls atomic.Int32
	e.weatherClient = &http.Client{Transport: weatherTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		q := request.URL.Query()
		if request.URL.Scheme != "https" || q.Get("current") != "temperature_2m,weather_code" || q.Get("timezone") != "auto" || q.Get("latitude") == "" || q.Get("longitude") == "" || q.Get("temperature_unit") != "celsius" {
			t.Error("invalid forecast query", request.URL)
		}
		return weatherResponse(200, weatherFixture), nil
	})}
	settings := weatherSettings()
	settings["noncommercial"] = false
	if _, err := e.weatherText(context.Background(), "conditions", settings); err == nil || calls.Load() != 0 {
		t.Fatal("consent must be checked before HTTP", err, calls.Load())
	}
	settings["noncommercial"] = true
	for action, want := range map[string]string{"conditions": "12.5 °C", "forecast": "8.0 °C\n15.0 °C", "sun": "07:30\n17:45"} {
		text, err := e.weatherText(context.Background(), action, settings)
		if err != nil || !strings.Contains(text, want) {
			t.Fatal(action, text, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("same location/actions should reuse one response", calls.Load())
	}
	other := weatherSettings()
	other["latitude"] = 40.7
	other["longitude"] = -74.0
	if _, err := e.weatherText(context.Background(), "conditions", other); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(e.weather) != 2 || settings["latitude"] != 51.5 {
		t.Fatal("locations were merged or settings mutated", calls.Load(), len(e.weather))
	}
	for _, bad := range []float64{91, math.Inf(1), math.NaN()} {
		other["latitude"] = bad
		if _, err := weatherURL(other); err == nil {
			t.Fatal("accepted invalid latitude")
		}
	}
}

// Verify weather failures do not cache empty data and use location bound fallback
func TestWeatherFailuresDoNotCacheEmptyDataAndUseLocationBoundFallback(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	status, body := 200, `{"error":true,"reason":"missing variables"}`
	e.weatherClient = &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) { return weatherResponse(status, body), nil })}
	settings := weatherSettings()
	if _, err := e.weatherText(context.Background(), "conditions", settings); err == nil || len(e.weather) != 0 {
		t.Fatal("cached a missing response", err)
	}
	status, body = 429, `{"reason":"Daily request limit exceeded"}`
	if _, err := e.weatherText(context.Background(), "conditions", settings); err == nil || !strings.Contains(err.Error(), "429: Daily request limit exceeded") {
		t.Fatal(err)
	}
	status, body = 200, weatherFixture
	if _, err := e.weatherText(context.Background(), "conditions", settings); err != nil {
		t.Fatal(err)
	}
	for key, value := range e.weather {
		value.At = time.Now().Add(-61 * time.Minute)
		e.weather[key] = value
	}
	status, body = 503, `{}`
	if text, err := e.weatherText(context.Background(), "conditions", settings); err != nil || !strings.HasPrefix(text, "12.5 °C") {
		t.Fatal(text, err)
	}
	other := weatherSettings()
	other["latitude"] = 40.7
	if text, err := e.weatherText(context.Background(), "conditions", other); err == nil || text != "" {
		t.Fatal("another location received cached weather", text, err)
	}
}

// Verify weather inspector and other locations cannot invalidate each others cache
func TestWeatherInspectorAndOtherLocationsCannotInvalidateEachOthersCache(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	london, ny := weatherSettings(), weatherSettings()
	ny["latitude"] = 40.7
	ny["longitude"] = -74.0
	londonURL, _ := weatherURL(london)
	nyURL, _ := weatherURL(ny)
	e.Bindings["london"] = binding{Action: "weather.conditions", Settings: london}
	e.Bindings["new-york"] = binding{Action: "weather.conditions", Settings: ny}
	e.weather = map[string]weatherCache{londonURL: {At: time.Now()}, nyURL: {At: time.Now()}}
	e.ClearContext("settings-draft:closed")
	if len(e.weather) != 2 {
		t.Fatal("closing an inspector cleared live locations")
	}
	e.Bindings["london-forecast"] = binding{Action: "weather.forecast", Settings: london}
	e.ClearContext("london")
	if len(e.weather) != 2 {
		t.Fatal("another binding still uses London")
	}
	delete(e.Bindings, "london-forecast")
	e.ClearContext("london")
	delete(e.Bindings, "london")
	if _, ok := e.weather[nyURL]; !ok || len(e.weather) != 2 {
		t.Fatal("removing a binding discarded reusable cache data")
	}
	existing := e.weather[nyURL]
	old := existing
	old.At = old.At.Add(-time.Hour)
	err := e.applyStatus(context.Background(), nil, jobResult{ID: "unrelated-probe", Value: probeStatus{Weather: map[string]weatherCache{londonURL: {At: time.Now()}, nyURL: old}}}, false)
	if err != nil || len(e.weather) != 2 || !e.weather[nyURL].At.Equal(existing.At) {
		t.Fatal("late result restored a removed location or replaced newer data", err)
	}
}

// Verify weather inspector test runs HTTP and returns result or failure
func TestWeatherInspectorTestRunsHTTPAndReturnsResultOrFailure(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			e.weatherClient = &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) {
				if status == 200 {
					return weatherResponse(status, weatherFixture), nil
				}
				return weatherResponse(status, `{"reason":"Access denied"}`), nil
			})}
			handled, err := e.extensionInspector(context.Background(), nil, sdk.Event{Event: "sendToPlugin", Context: "draft", Action: "app.streamdockmanager.plugins.weather.conditions", Payload: []byte(`{"op":"test","settings":{"latitude":51.5,"longitude":-0.12,"noncommercial":true}}`)})
			if !handled || err != nil {
				t.Fatal(handled, err)
			}
			select {
			case result := <-e.Completed:
				preview := result.Value.(map[string]any)
				if preview["available"] != (status == 200) {
					t.Fatal(preview, result.Err)
				}
				if status == 200 && !strings.Contains(preview["message"].(string), "12.5 °C") {
					t.Fatal(preview)
				}
				if status != 200 && (result.Err == nil || !strings.Contains(preview["message"].(string), "403")) {
					t.Fatal(preview, result.Err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Weather Test made no request/result")
			}
		})
	}
}

// Verify weather live noncommercial forecast
func TestWeatherLiveNoncommercialForecast(t *testing.T) {
	if os.Getenv("STREAM_DOCK_MANAGER_WEATHER_LIVE") != "1" {
		t.Skip("explicit public-network weather check")
	}
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	for _, action := range []string{"conditions", "forecast", "sun"} {
		text, err := e.weatherText(context.Background(), action, weatherSettings())
		if err != nil {
			t.Fatal(action, err)
		}
		t.Log(action, text)
	}
}

// Delegate the fixture HTTP request to its injected response handler
func (f weatherTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type weatherTransport func(*http.Request) (*http.Response, error)

const weatherFixture = `{"current":{"temperature_2m":12.5,"weather_code":2},"daily":{"time":["2026-10-25"],"temperature_2m_max":[15.0],"temperature_2m_min":[8.0],"sunrise":["2026-10-25T07:30"],"sunset":["2026-10-25T17:45"]}}`

// Build a deterministic forecast response for the test service
func weatherResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// Build location settings for the forecast fixture
func weatherSettings() map[string]any {
	return map[string]any{"latitude": 51.5, "longitude": -0.12, "noncommercial": true, "units": "celsius"}
}

type Engine = Plugin

var Definitions = []engine.Definition{Definition}
