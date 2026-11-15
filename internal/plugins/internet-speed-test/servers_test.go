package internetspeedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/showwin/speedtest-go/speedtest"
)

// Verify global server controls every run
func TestGlobalServerControlsEveryRun(t *testing.T) {
	for _, test := range []struct {
		name   string
		global map[string]any
		want   string
	}{
		{"automatic", map[string]any{"server_mode": "auto", "server": "stored-manual"}, ""},
		{"manual", map[string]any{"server_mode": "manual", "server": "12345"}, "12345"},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			e.Global = test.global
			selected := make(chan string, 2)
			e.speed = func(ctx context.Context, id string, _ func(SpeedUpdate)) (SpeedResult, error) {
				selected <- id
				<-ctx.Done()
				return SpeedResult{}, ctx.Err()
			}
			info, _ := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1"]}}`))
			client, events := sdk.NewLocalClient(info, "fixture")
			for _, id := range []string{"first", "second"} {
				e.Bindings[id] = binding{Action: "app.streamdockmanager.plugins.internet-speed-test.run", Settings: map[string]any{"server": "legacy"}}
				if err := e.Handle(context.Background(), client, sdk.Event{Event: "keyDown", Context: id, Action: "app.streamdockmanager.plugins.internet-speed-test.run"}); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-selected:
					if got != test.want {
						t.Fatalf("server=%q, want %q", got, test.want)
					}
				case <-time.After(time.Second):
					t.Fatal("run did not start")
				}
				e.cancelSpeed()
				for len(events) > 0 {
					<-events
				}
			}
		})
	}
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Global = map[string]any{"server_mode": "manual"}
	if _, err := e.selectedServer(); err == nil {
		t.Fatal("missing global selection silently used action override")
	}
}

// Verify automatic selection uses lowest reachable nearby ping
func TestAutomaticSelectionUsesLowestReachableNearbyPing(t *testing.T) {
	servers := speedtest.Servers{
		{ID: "closest", Distance: 1, Latency: 20 * time.Millisecond},
		{ID: "faster", Distance: 2, Latency: 1500 * time.Microsecond},
		{ID: "offline", Distance: 3, Latency: speedtest.PingTimeout},
		{ID: "slower", Distance: 4, Latency: 1900 * time.Microsecond},
		{ID: "offline-again", Distance: 5, Latency: speedtest.PingTimeout},
		{ID: "distant", Distance: 1000, Latency: time.Microsecond},
	}
	got, err := lowestNearbyServer(servers)
	if err != nil || got.ID != "faster" {
		t.Fatalf("selected %+v: %v", got, err)
	}
	for _, candidates := range []speedtest.Servers{nil, {{Latency: speedtest.PingTimeout}}} {
		if _, err := lowestNearbyServer(candidates); err == nil {
			t.Fatal("unreachable/unmeasured candidates accepted")
		}
	}
	if _, err := lowestNearbyServer(speedtest.Servers{{ID: "fast", Latency: 0}}); err != nil {
		t.Fatal("successful ping rounded to zero was rejected", err)
	}
}

// Verify server discovery only lists and pings and cancels with its parent
func TestServerDiscoveryOnlyListsAndPingsAndCancelsWithItsParent(t *testing.T) {
	var base string
	var block atomic.Bool
	var started sync.Once
	pingStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/js/servers":
			fmt.Fprintf(w, `[{"id":"1","name":"Nearby","sponsor":"Fixture","country":"Test","url":%q,"distance":1},{"id":"2","name":"Next","sponsor":"Fixture","country":"Test","url":%q,"distance":2}]`, base+"/speedtest/upload.php", base+"/speedtest/upload.php")
		case "/speedtest/latency.txt":
			if block.Load() {
				started.Do(func() { close(pingStarted) })
				<-r.Context().Done()
				return
			}
			io.WriteString(w, "test=test")
		default:
			t.Errorf("discovery started a transfer: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	endpoint, _ := url.Parse(base)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doer := server.Client()
	next := doer.Transport
	client := speedtest.New(speedtest.WithDoer(doer), speedtest.WithUserConfig(&speedtest.UserConfig{UserAgent: speedtest.DefaultUserAgent}))
	doer.Transport = parentSpeedTransport{ctx, speedTransportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		target := *r.URL
		target.Scheme, target.Host = endpoint.Scheme, endpoint.Host
		copy.URL = &target
		return next.RoundTrip(copy)
	})}
	result, err := discoverServerChoices(ctx, client)
	if err != nil || len(result.Servers) != 2 || result.Recommended == "" {
		t.Fatalf("discovery=%+v err=%v", result, err)
	}
	block.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := discoverServerChoices(ctx, client)
		done <- err
	}()
	select {
	case <-pingStarted:
	case <-time.After(time.Second):
		t.Fatal("candidate ping did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("candidate pings outlived cancelled discovery")
	}
}

// Verify discovery replies stay bound to their inspector request
func TestDiscoveryRepliesStayBoundToTheirInspectorRequest(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.discoverServers = func(context.Context) (serverDiscovery, error) {
		return serverDiscovery{Servers: []serverChoice{{ID: "7", Name: "Fixture"}}, Recommended: "7"}, nil
	}
	client, events := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
	ev := sdk.Event{Event: "sendToPlugin", Context: "global-draft", Payload: json.RawMessage(`{"op":"speed_servers","request_id":"42","scope":"plugin"}`)}
	if handled, err := e.extensionInspector(context.Background(), client, ev); !handled || err != nil {
		t.Fatal(handled, err)
	}
	select {
	case <-e.WakeChannel:
	case <-time.After(time.Second):
		t.Fatal("no discovery completion")
	}
	if err := e.DrainJobs(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	reply := <-events
	var payload struct {
		Op, RequestID string
		Available     bool
		Recommended   string
	}
	var raw map[string]any
	json.Unmarshal(reply.Payload, &raw)
	json.Unmarshal(reply.Payload, &payload)
	if reply.Event != "sendToPropertyInspector" || reply.Context != "global-draft" || raw["request_id"] != "42" || payload.Op != "speed_servers" || !payload.Available || payload.Recommended != "7" {
		t.Fatalf("reply=%+v %s", reply, reply.Payload)
	}
}

// Verify missing modern server mode does not use action settings
func TestMissingModernServerModeDoesNotUseActionSettings(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	if _, err := e.selectedServer(); err == nil {
		t.Fatal("obsolete action-local server accepted")
	}
}
