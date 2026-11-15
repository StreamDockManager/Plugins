package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// Verify saved locations resolve without mutating independent action settings
func TestSavedLocationsResolveWithoutMutatingIndependentActionSettings(t *testing.T) {
	settings := weatherSettings()
	settings["location_id"] = "home"
	settings["units"] = "fahrenheit"
	globals := map[string]any{"saved_locations": []any{map[string]any{"id": "home", "name": "Home", "latitude": 40.7, "longitude": -74.0}}}
	resolved := resolveLocation(settings, globals)
	if resolved["latitude"] != 40.7 || resolved["units"] != "fahrenheit" || settings["latitude"] != 51.5 {
		t.Fatal(settings, resolved)
	}
	missing := resolveLocation(settings, map[string]any{})
	if missing["latitude"] != settings["latitude"] || missing["location_id"] != "home" {
		t.Fatal("lost portable fallback", missing)
	}
}

// Verify forecast cache survives restart and honors intervals
func TestForecastCacheSurvivesRestartAndHonorsIntervals(t *testing.T) {
	info, _ := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-storage-v1"]}}`))
	client, events := sdk.NewLocalClient(info, "weather-cache-test")
	done := make(chan struct{})
	defer close(done)
	records := map[string]json.RawMessage{}
	go func() {
		revision := 0
		for {
			select {
			case <-done:
				return
			case event := <-events:
				var payload map[string]any
				json.Unmarshal(event.Payload, &payload)
				result := map[string]any{"generation": "1", "revision": fmt.Sprint(revision)}
				switch event.Method {
				case "listKeys":
					keys := []string{}
					for key := range records {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					result["keys"] = keys
				case "get":
					data, ok := records[payload["key"].(string)]
					result["found"] = ok
					if ok {
						result["value"] = data
					}
				case "commit":
					for _, value := range payload["mutations"].([]any) {
						mutation := value.(map[string]any)
						key := mutation["key"].(string)
						if mutation["op"] == "delete" {
							delete(records, key)
						} else {
							records[key], _ = json.Marshal(mutation["value"])
						}
					}
					revision++
					result["revision"] = fmt.Sprint(revision)
					result["committed"] = true
					result["operation_id"] = payload["operation_id"]
				default:
					continue
				}
				raw, _ := json.Marshal(result)
				client.ObserveControl(sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: event.ProtocolID, RequestID: event.RequestID, Result: raw})
			}
		}
	}()
	first := New(filepath.Join(t.TempDir(), "state.json"))
	first.loadForecastCache(context.Background(), client)
	var data map[string]any
	json.Unmarshal([]byte(weatherFixture), &data)
	settings := weatherSettings()
	endpoint, _ := weatherURL(settings)
	first.weather[endpoint] = weatherCache{At: time.Now().Add(-30 * time.Minute), Data: data}
	first.persistForecast(context.Background(), endpoint)
	first.Close()
	second := New(filepath.Join(t.TempDir(), "state.json"))
	defer second.Close()
	second.loadForecastCache(context.Background(), client)
	calls := 0
	second.weatherClient = &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) { calls++; return weatherResponse(200, weatherFixture), nil })}
	if text, err := second.weatherText(context.Background(), "conditions", settings); err != nil || text != "12.5 °C" || calls != 0 {
		t.Fatal("hourly cache was not reused", text, err, calls)
	}
	settings["update_interval_ms"] = 60000
	if _, err := second.weatherText(context.Background(), "conditions", settings); err != nil || calls != 1 {
		t.Fatal("custom interval did not refresh", err, calls)
	}
	settings["update_interval_ms"] = 0
	cached := second.weather[endpoint]
	cached.At = time.Now().Add(-2 * time.Hour)
	second.weather[endpoint] = cached
	second.weatherText(context.Background(), "conditions", settings)
	if calls != 1 {
		t.Fatal("Off fetched periodically")
	}
	cached.Refresh = true
	second.weather[endpoint] = cached
	second.weatherText(context.Background(), "conditions", settings)
	if calls != 2 {
		t.Fatal("manual invalidation did not refresh Off")
	}
	cached = second.weather[endpoint]
	cached.RetryAt = time.Now().Add(time.Minute)
	second.weather[endpoint] = cached
	second.persistForecast(context.Background(), endpoint)
	third := New(filepath.Join(t.TempDir(), "state.json"))
	defer third.Close()
	third.loadForecastCache(context.Background(), client)
	third.weatherClient = second.weatherClient
	cached = third.weather[endpoint]
	cached.Refresh = true
	third.weather[endpoint] = cached
	text, err := third.weatherText(context.Background(), "conditions", settings)
	if err != nil || text != "12.5 °C" || third.lastWeatherError == nil || calls != 2 {
		t.Fatal("restart lost cooldown or last-good fallback", text, err, calls)
	}
}

// Verify forecast cache evicts least recently used and bounds bytes
func TestForecastCacheEvictsLeastRecentlyUsedAndBoundsBytes(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.weather = map[string]weatherCache{}
	for i := range 129 {
		key := fmt.Sprintf("https://weather.invalid/%d", i)
		e.weather[key] = weatherCache{At: time.Now(), Used: time.Unix(int64(i), 0), Data: map[string]any{"value": i}}
	}
	e.persistForecast(context.Background(), "https://weather.invalid/128")
	if len(e.weather) != 128 {
		t.Fatal(len(e.weather))
	}
	if _, exists := e.weather["https://weather.invalid/0"]; exists {
		t.Fatal("oldest entry retained")
	}
	for key, value := range e.weather {
		value.Data = map[string]any{"padding": strings.Repeat("x", 200000)}
		e.weather[key] = value
	}
	e.persistForecast(context.Background(), "https://weather.invalid/128")
	total := len(forecastCacheNamespace)
	for key, value := range e.weather {
		data, _ := json.Marshal(storedForecast{key, value})
		total += len(data) + len(forecastKey(key))
	}
	if total > forecastCacheBytes || len(e.weather) >= 128 {
		t.Fatal("cache byte bound exceeded", total, len(e.weather))
	}
}

// Verify global location edit invalidates only linked bindings
func TestGlobalLocationEditInvalidatesOnlyLinkedBindings(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.cacheLoaded = true
	locations := []map[string]any{{"id": "home", "name": "Home", "latitude": 40.7, "longitude": -74.0}, {"id": "work", "name": "Work", "latitude": 51.5, "longitude": -0.1}}
	e.Global = map[string]any{"saved_locations": locations}
	for _, id := range []string{"home", "work", "custom"} {
		settings := weatherSettings()
		if id != "custom" {
			settings["location_id"] = id
		}
		e.Bindings[id] = binding{Action: "app.streamdockmanager.plugins.weather.conditions", Settings: settings}
		e.Effective[id] = resolveLocation(settings, e.Global)
		e.ProbeKeys[id] = "status:" + id
	}
	next := []map[string]any{cloneSettings(locations[0]), cloneSettings(locations[1])}
	next[0]["latitude"] = 45.0
	event := sdk.Message("didReceiveGlobalSettings", "", map[string]any{"settings": map[string]any{"saved_locations": next}})
	if handled, err := e.beforeEvent(context.Background(), nil, event); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if e.Effective["home"] != nil || e.ProbeKeys["home"] != "" {
		t.Fatal("linked location was not invalidated")
	}
	for _, id := range []string{"work", "custom"} {
		if e.Effective[id] == nil || e.ProbeKeys[id] == "" {
			t.Fatal("unaffected request invalidated", id)
		}
	}
	e.Effective["home"] = resolveLocation(e.Bindings["home"].Settings, e.Global)
	event = sdk.Message("didReceiveGlobalSettings", "", map[string]any{"settings": map[string]any{"saved_locations": next[1:]}})
	e.beforeEvent(context.Background(), nil, event)
	if e.Effective["home"] != nil || resolveLocation(e.Bindings["home"].Settings, e.Global)["latitude"] != 51.5 {
		t.Fatal("deletion did not restore portable snapshot")
	}
}
