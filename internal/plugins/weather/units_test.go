package weather

import (
	"encoding/json"
	"github.com/StreamDockManager/Plugin-SDK/Go"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Verify temperature settings reach request cache and live display
func TestTemperatureSettingsReachRequestCacheAndLiveDisplay(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	var requests []string
	e.weatherClient = &http.Client{Transport: weatherTransport(func(r *http.Request) (*http.Response, error) {
		units := r.URL.Query().Get("temperature_unit")
		requests = append(requests, units)
		body := weatherFixture
		if units == "fahrenheit" {
			body = strings.NewReplacer("12.5", "54.5", "15.0", "59.0", "8.0", "46.4").Replace(body)
		}
		return weatherResponse(200, body), nil
	})}
	var steps []displayStep
	for i, units := range []string{"celsius", "fahrenheit", "celsius"} {
		settings := weatherSettings()
		settings["units"] = units
		payload, _ := json.Marshal(map[string]any{"settings": settings})
		kind := "didReceiveSettings"
		if i == 0 {
			kind = "willAppear"
		}
		steps = append(steps, displayStep{event: sdk.Event{Event: kind, Context: "key", Action: "app.streamdockmanager.plugins.weather.conditions", Payload: payload}})
	}
	all := driveDisplays(t, e, steps)
	for i, want := range []string{"12.5 °C", "54.5 °F", "12.5 °C"} {
		title := ""
		for _, ev := range all[i] {
			if ev.Event == "setTitle" {
				var p struct{ Title string }
				_ = json.Unmarshal(ev.Payload, &p)
				title = p.Title
			}
		}
		if !strings.Contains(title, want) {
			t.Fatalf("step %d: wanted %q, got %q", i, want, title)
		}
	}
	if strings.Join(requests, ",") != "celsius,fahrenheit" {
		t.Fatal("incorrect request units", requests)
	}
}
