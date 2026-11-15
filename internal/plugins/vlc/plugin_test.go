package vlc

import (
	"context"
	"fmt"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Verify authenticated discovery and serialized dial adjustments
func TestAuthenticatedDiscoveryAndSerializedDialAdjustments(t *testing.T) {
	volume := 250.0
	position := 5.0
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || password != "secret" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "playlist.json") {
			fmt.Fprint(w, `{"children":[{"id":"3","name":"Track"}]}`)
			return
		}
		switch r.URL.Query().Get("command") {
		case "volume":
			fmt.Sscan(r.URL.Query().Get("val"), &volume)
			writes++
		case "seek":
			fmt.Sscan(r.URL.Query().Get("val"), &position)
			writes++
		}
		fmt.Fprintf(w, `{"rate":1,"state":"playing","volume":%v,"time":%v,"length":100,"information":{"category":{"meta":{"title":"Song"},"Stream 0":{"Type":"Audio","Description":"English"}}}}`, volume, position)
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP()}
	defer d.Close()
	s := map[string]any{"endpoint": server.URL, "password": "secret", "step": 10.0}
	ctx := context.Background()
	choices, err := d.Discover(ctx, s)
	if err != nil || len(choices) != 3 || writes != 0 {
		t.Fatal(choices, err, writes)
	}
	for _, ticks := range []int{1, -1} {
		if _, err = d.Command(ctx, "volume", s, ticks); err != nil {
			t.Fatal(err)
		}
	}
	if volume != 230 {
		t.Fatal("limits or fresh read failed", volume)
	}
	if _, err = d.Command(ctx, "seek", s, -4); err != nil || position != 0 {
		t.Fatal(position, err)
	}
	if _, err = d.Command(ctx, "seek", s, 100); err != nil || position != 100 {
		t.Fatal(position, err)
	}
}

// Verify authentication and malformed responses
func TestAuthenticationAndMalformedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bad") == "" {
			w.WriteHeader(401)
		}
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP()}
	defer d.Close()
	_, err := d.Read(context.Background(), map[string]any{"endpoint": server.URL, "password": "secret"})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

// Verify progress uses sample clock and paused cadence
func TestProgressUsesSampleClockAndPausedCadence(t *testing.T) {
	now := time.Unix(1000, 0)
	reads := 0
	state := "playing"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		fmt.Fprintf(w, `{"state":%q,"rate":1,"volume":128,"time":95,"length":100}`, state)
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP(), Now: func() time.Time { return now }}
	defer d.Close()
	s := map[string]any{"endpoint": server.URL, "password": "fixture"}
	ctx := context.Background()
	value, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if text := d.Display(value, "progress", s); !strings.Contains(text, "98s / 100s") {
		t.Fatal(text)
	}
	if _, err = d.Read(ctx, s); err != nil || reads != 1 {
		t.Fatal("progress caused another request", reads, err)
	}
	now = now.Add(10 * time.Second)
	if text := d.Display(value, "progress", s); !strings.Contains(text, "100s / 100s") {
		t.Fatal("progress exceeded length", text)
	}
	state = "paused"
	value, err = d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(20 * time.Second)
	if _, err = d.Read(ctx, s); err != nil || reads != 2 {
		t.Fatal("paused playback polled early", reads, err)
	}
	if text := d.Display(value, "progress", s); !strings.Contains(text, "95s / 100s") {
		t.Fatal("paused progress advanced", text)
	}
	if _, err = d.Command(ctx, "progress", s, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Read(ctx, s); err != nil || reads != 4 {
		t.Fatal("command did not refresh playback", reads, err)
	}
}

// Verify external playback change and track reset at next sample
func TestExternalPlaybackChangeAndTrackResetAtNextSample(t *testing.T) {
	now := time.Unix(1000, 0)
	state := "playing"
	position := 50.0
	track := "First"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"state":%q,"volume":128,"time":%v,"length":100,"information":{"category":{"meta":{"title":%q}}}}`, state, position, track)
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP(), Now: func() time.Time { return now }}
	defer d.Close()
	settings := map[string]any{"endpoint": server.URL, "password": "fixture"}
	first, err := d.Read(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	state = "paused"
	position = 2
	track = "Second"
	now = now.Add(5 * time.Second)
	second, err := d.Read(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if text := d.Display(second, "track-info", settings); !strings.Contains(text, "Second") || !strings.Contains(text, "paused") {
		t.Fatal("external track/state update was missed", text)
	}
	if text := d.Display(second, "progress", settings); !strings.Contains(text, "2s / 100s") {
		t.Fatal("old track progress carried forward", text)
	}
	if *first.(Status).Time != 50 || first.(Status).State != "playing" {
		t.Fatal("new sample mutated prior snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.Read(ctx, settings); err != context.Canceled {
		t.Fatal("cancelled read returned cached success", err)
	}
}
