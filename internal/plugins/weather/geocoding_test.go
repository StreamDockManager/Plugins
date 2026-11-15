package weather

import (
	"context"
	"encoding/json"
	"github.com/StreamDockManager/Plugin-SDK/Go"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verify location search preserves postal codes and separates address queries
func TestLocationSearchPreservesPostalCodesAndSeparatesAddressQueries(t *testing.T) {
	for _, query := range []string{"02108", "SW1A 1AA", "H2X 1Y4"} {
		endpoint, err := geocodingURL("https://geo.example/search", false, LocationSearch{"postal", query, "US"})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(endpoint)
		if u.Query().Get("postalcode") != query || u.Query().Has("q") || u.Query().Get("countrycodes") != "us" {
			t.Fatal(endpoint)
		}
	}
	endpoint, err := geocodingURL("https://geo.example/search", false, LocationSearch{"address", "Public Library, Boston", "us"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(endpoint)
	if u.Query().Has("postalcode") || u.Query().Get("q") == "" {
		t.Fatal(endpoint)
	}
	for _, endpoint := range []string{"http://geo.example/search", "https://user:pass@geo.example/search", "https://geo.example/search?q=secret"} {
		if _, err := geocodingURL(endpoint, false, LocationSearch{"postal", "02108", "us"}); err == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	if _, err := geocodingURL("https://broker.example/search", true, LocationSearch{"address", "home address", "us"}); err == nil {
		t.Fatal("address sent to postal-only service")
	}
}

// Verify location search cache errors and cancellation
func TestLocationSearchCacheErrorsAndCancellation(t *testing.T) {
	calls := 0
	body := `[{"display_name":"Boston","lat":"42.35","lon":"-71.06"}]`
	status := 200
	g := geocoder{client: &http.Client{Transport: weatherTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.Contains(r.Header.Get("User-Agent"), "stream-dock-manager") {
			t.Fatal("missing identity")
		}
		return weatherResponse(status, body), nil
	})}}
	ctx := context.Background()
	search := LocationSearch{"postal", "02108", "us"}
	results, err := g.search(ctx, "https://geo.example/search", false, search)
	if err != nil || len(results) != 1 {
		t.Fatal(results, err)
	}
	results[0].Label = "mutated"
	again, err := g.search(ctx, "https://geo.example/search", false, search)
	if err != nil || again[0].Label != "Boston" || calls != 1 {
		t.Fatal(again, err, calls)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.search(cancelled, "https://geo.example/search", false, search); err != context.Canceled {
		t.Fatal(err)
	}
	for _, invalid := range []string{`[{"display_name":"bad","lat":"NaN","lon":"0"}]`, `[{"display_name":"bad","lat":"0","lon":"181"}]`, `{"error":"bad"}`, `[] trailing`} {
		clear(g.cache)
		body = invalid
		if _, err := g.search(ctx, "https://geo.example/search", false, search); err == nil || len(g.cache) != 0 {
			t.Fatal("invalid response cached", invalid, err)
		}
	}
	body = `[]`
	clear(g.cache)
	if result, err := g.search(ctx, "https://geo.example/search", false, search); err != nil || len(result) != 0 {
		t.Fatal(result, err)
	}
	if len(g.cache) != 1 {
		t.Fatal("negative result not cached")
	}
	for k, v := range g.cache {
		v.At = time.Now().Add(-25 * time.Hour)
		g.cache[k] = v
	}
	status = 429
	if _, err := g.search(ctx, "https://geo.example/search", false, search); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatal(err)
	}
}

// Verify location inspector uses plugin endpoint and correlates transient results
func TestLocationInspectorUsesPluginEndpointAndCorrelatesTransientResults(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Global = map[string]any{"geocoding_endpoint": "https://configured.example/search"}
	e.geocoding = &geocoder{client: &http.Client{Transport: weatherTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "configured.example" || r.URL.Query().Get("postalcode") != "02108" {
			t.Error(r.URL)
		}
		return weatherResponse(200, `[{"display_name":"Boston","lat":"42.35","lon":"-71.06"}]`), nil
	})}}
	client, events := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
	handled, err := e.extensionInspector(context.Background(), client, sdk.Event{Event: "sendToPlugin", Context: "draft", Payload: []byte(`{"op":"search_location","request_id":"7","search":{"mode":"postal","query":"02108","country":"us"},"settings":{"geocoding_endpoint":"https://untrusted.example/search"}}`)})
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	select {
	case result := <-e.Completed:
		if err := e.ApplyJobResult(context.Background(), client, result); err != nil {
			t.Fatal(err)
		}
		event := <-events
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["request_id"] != "7" || payload["op"] != "location_results" || payload["available"] != true {
			t.Fatal(payload)
		}
		if len(e.Bindings) != 0 || len(e.weather) != 0 {
			t.Fatal("lookup changed binding or forecast state")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no location response")
	}
}

// Verify location search defaults to public endpoint and persists cache across runs
func TestLocationSearchDefaultsToPublicEndpointAndPersistsCacheAcrossRuns(t *testing.T) {
	for _, endpoint := range []string{"", publicGeocodingEndpoint, "https://NOMINATIM.OPENSTREETMAP.ORG./search"} {
		value, err := geocodingURL(endpoint, false, LocationSearch{"address", "Boston Public Library", "us"})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(value)
		if !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "nominatim.openstreetmap.org") || u.Query().Get("q") != "Boston Public Library" {
			t.Fatal(value)
		}
	}
	path := filepath.Join(t.TempDir(), "cache.v1.json")
	calls := 0
	client := &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return weatherResponse(200, `[{"display_name":"Boston","lat":"42.35","lon":"-71.06"}]`), nil
	})}
	search := LocationSearch{"postal", "02108", "us"}
	first := geocoder{path: path, client: client}
	if _, err := first.search(context.Background(), "", false, search); err != nil {
		t.Fatal(err)
	}
	second := geocoder{path: path, client: client}
	result, err := second.search(context.Background(), "", false, search)
	if err != nil || len(result) != 1 || calls != 1 {
		t.Fatal(result, err, calls)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "02108") {
		t.Fatal("raw query stored", err)
	}
}

// Verify public location search throttles and honors retry after
func TestPublicLocationSearchThrottlesAndHonorsRetryAfter(t *testing.T) {
	calls := 0
	var at []time.Time
	g := geocoder{client: &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) {
		calls++
		at = append(at, time.Now())
		response := weatherResponse(429, `{}`)
		response.Header = http.Header{"Retry-After": {"60"}}
		return response, nil
	})}}
	if _, err := g.search(context.Background(), "", false, LocationSearch{"postal", "02108", "us"}); err == nil {
		t.Fatal("429 accepted")
	}
	if _, err := g.search(context.Background(), "", false, LocationSearch{"postal", "10001", "us"}); err == nil || calls != 1 {
		t.Fatal("Retry-After ignored", calls, err)
	}
	g.cooldown = time.Time{}
	g.client.Transport = weatherTransport(func(*http.Request) (*http.Response, error) {
		calls++
		at = append(at, time.Now())
		return weatherResponse(200, `[]`), nil
	})
	if _, err := g.search(context.Background(), "", false, LocationSearch{"postal", "10001", "us"}); err != nil {
		t.Fatal(err)
	}
	if at[1].Sub(at[0]) < time.Second {
		t.Fatal("public requests not spaced", at)
	}
	g.next = time.Now().Add(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.search(ctx, "", false, LocationSearch{"postal", "90210", "us"}); err != context.Canceled {
		t.Fatal(err)
	}
}
