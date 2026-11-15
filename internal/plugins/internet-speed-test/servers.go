package internetspeedtest

import (
	"context"
	"encoding/json"
	"fmt"
	discovery "github.com/StreamDockManager/Plugin-SDK/Go/discovery"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/showwin/speedtest-go/speedtest"
)

type serverChoice struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	LatencyMS  *float64 `json:"latency_ms,omitempty"`
	DistanceKM float64  `json:"distance_km"`
}

type serverDiscovery struct {
	Servers     []serverChoice `json:"servers"`
	Recommended string         `json:"recommended"`
}

// Resolve and validate the configured speed-test server
func (e *Plugin) selectedServer() (string, error) {
	mode, configured := e.Global["server_mode"]
	if !configured {
		return "", fmt.Errorf("configure server selection in Plugin settings before running a speed test")
	}
	switch mode {
	case "auto":
		return "", nil
	case "manual":
		id := strings.TrimSpace(configvalue.String(e.Global, "server", ""))
		if id == "" || len(id) > 64 {
			return "", fmt.Errorf("choose a speed-test server in Plugin settings")
		}
		return id, nil
	default:
		return "", fmt.Errorf("invalid speed-test server selection")
	}
}

// Discovery has already measured these candidates. Compare exact durations,
// exclude failed pings, and only consider the five geographically nearest
func lowestNearbyServer(servers speedtest.Servers) (*speedtest.Server, error) {
	nearby := append(speedtest.Servers(nil), servers...)
	sort.SliceStable(nearby, func(i, j int) bool { return nearby[i].Distance < nearby[j].Distance })
	var best *speedtest.Server
	for _, server := range nearby[:min(5, len(nearby))] {
		if server.Latency < 0 {
			continue
		}
		if best == nil || server.Latency < best.Latency {
			best = server
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no reachable nearby speed-test servers; load servers and choose one manually")
	}
	return best, nil
}

// Discover speed-test servers for the selected configuration
func discoverSpeedServers(parent context.Context) (serverDiscovery, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	client, closeClient := newSpeedClient(ctx)
	defer closeClient()
	return discoverServerChoices(ctx, client)
}

// Build bounded inspector choices from available speed-test servers
func discoverServerChoices(ctx context.Context, client *speedtest.Speedtest) (serverDiscovery, error) {
	servers, err := client.FetchServerListContext(ctx)
	if err := ctx.Err(); err != nil {
		return serverDiscovery{}, err
	}
	if err != nil {
		return serverDiscovery{}, fmt.Errorf("could not load speed-test servers; check the connection and retry")
	}
	result := serverDiscovery{Servers: []serverChoice{}}
	for _, server := range servers[:min(100, len(servers))] {
		if server.ID == "" || len(server.ID) > 64 {
			continue
		}
		short := func(value string) string {
			runes := []rune(strings.TrimSpace(value))
			return string(runes[:min(120, len(runes))])
		}
		choice := serverChoice{ID: server.ID, Name: fmt.Sprintf("%s — %s, %s", short(server.Sponsor), short(server.Name), short(server.Country)), DistanceKM: server.Distance}
		if server.Latency >= 0 {
			latency := float64(server.Latency) / float64(time.Millisecond)
			choice.LatencyMS = &latency
		}
		result.Servers = append(result.Servers, choice)
	}
	if best, err := lowestNearbyServer(servers); err == nil {
		result.Recommended = best.ID
	}
	if len(result.Servers) == 0 {
		return result, fmt.Errorf("no speed-test servers available")
	}
	return result, nil
}

// Discover speed-test servers for the selected configuration
func (e *Plugin) discoverSpeedServers(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	var request struct {
		RequestID string `json:"request_id"`
		Scope     string `json:"scope"`
	}
	if json.Unmarshal(ev.Payload, &request) != nil || request.RequestID == "" || len(request.RequestID) > 80 || request.Scope != "plugin" {
		return fmt.Errorf("invalid speed-test server discovery request")
	}
	return e.discovery.Run(ctx, e.Engine, c, discovery.Request{Context: ev.Context, Action: ev.Action, Channel: "speed_servers", ID: request.RequestID, Operation: "speed_servers", Scope: request.Scope}, 20*time.Second, func(job context.Context) (map[string]any, error) {
		result, err := e.discoverServers(job)
		message := "Choose a server, or use automatic detection for each test."
		if err != nil {
			message = err.Error()
		}
		return map[string]any{"servers": result.Servers, "recommended": result.Recommended, "message": message}, err
	})
}

// The dependency uses a background context for its initial candidate pings
// Bind their HTTP lifetime to our operation as well as its per-request context
type parentSpeedTransport struct {
	parent context.Context
	next   http.RoundTripper
}
type speedResponseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

// Close the response body and release its parent-cancellation linkage
func (b *speedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

// Forward a speed-test request with its required cancellation context
func (t parentSpeedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.parent, cancel)
	// Keep parent cancellation linked until the response body is closed
	release := func() { stop(); cancel() }
	if err := t.parent.Err(); err != nil {
		release()
		return nil, err
	}
	response, err := t.next.RoundTrip(request.Clone(ctx))
	if err != nil {
		release()
		return nil, err
	}
	if request.URL.Path == "/api/js/servers" {
		if response.ContentLength > 1<<20 {
			response.Body.Close()
			release()
			return nil, fmt.Errorf("speed-test server list exceeds 1 MiB")
		}
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, 1<<20), response.Body}
	}
	response.Body = &speedResponseBody{ReadCloser: response.Body, release: release}
	return response, nil
}

// Release idle connections held by the wrapped speed-test transport
func (t parentSpeedTransport) CloseIdleConnections() {
	if next, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		next.CloseIdleConnections()
	}
}
