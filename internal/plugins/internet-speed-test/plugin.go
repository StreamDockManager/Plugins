package internetspeedtest

import (
	"context"
	"encoding/json"
	"fmt"
	discovery "github.com/StreamDockManager/Plugin-SDK/Go/discovery"
	"net/http"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/httptrack"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/showwin/speedtest-go/speedtest"
)

type Plugin struct {
	discovery discovery.Runner
	*engine.Engine
	speedContext    string
	speedDisplay    map[string]string
	speedAction     string
	speed           SpeedRunner
	discoverServers func(context.Context) (serverDiscovery, error)
	progress        chan speedProgress
	State           map[string]*saved
}

type SpeedResult struct {
	At           string  `json:"at"`
	Server       string  `json:"server"`
	LatencyMS    float64 `json:"latency_ms"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
}

type SpeedRunner func(context.Context, string, func(SpeedUpdate)) (SpeedResult, error)

type SpeedUpdate struct {
	Action   string
	Text     string
	Complete bool
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type saved struct {
	SpeedResults []SpeedResult `json:"speed_results,omitempty"`
	Value        float64       `json:"value"`
	Deadline     int64         `json:"deadline"`
}

type speedProgress struct {
	Generation uint64
	Update     SpeedUpdate
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var emptySaved saved

// Attach internet speed test lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Run a cancellable speed measurement and report intermediate progress
func RunSpeedTest(parent context.Context, id string, progress func(SpeedUpdate)) (SpeedResult, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	client, closeClient := newSpeedClient(ctx)
	defer closeClient()
	return runSpeedTest(ctx, id, progress, client)
}

// Create a speed-test client whose requests follow parent cancellation
func newSpeedClient(parent context.Context) (*speedtest.Speedtest, func()) {
	doer := &http.Client{}
	client := speedtest.New(speedtest.WithDoer(doer), speedtest.WithUserConfig(&speedtest.UserConfig{UserAgent: speedtest.DefaultUserAgent}))
	*doer = *httptrack.DebugHTTPClient(doer)
	doer.Transport = cancelledSpeedTransport{parentSpeedTransport{parent, doer.Transport}}
	return client, doer.CloseIdleConnections
}

// speedtest-go keeps retrying until its capture timer ends, even after context
// cancellation. Pace only those cancelled retries so its retained chunk metadata
// and worker CPU stay bounded while the dependency finishes the current phase
// Live requests retain the transport's normal throughput and cancellation behavior
type cancelledSpeedTransport struct{ next http.RoundTripper }

// Forward a speed-test request with its required cancellation context
func (t cancelledSpeedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		time.Sleep(10 * time.Millisecond)
		return nil, err
	}
	return t.next.RoundTrip(request)
}

// Release idle connections held by the wrapped speed-test transport
func (t cancelledSpeedTransport) CloseIdleConnections() {
	if next, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		next.CloseIdleConnections()
	}
}

// Select the server and measure latency, download and upload throughput
func runSpeedTest(parent context.Context, id string, progress func(SpeedUpdate), client *speedtest.Speedtest) (SpeedResult, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	progress(SpeedUpdate{Action: "run", Text: "Selecting server · 0%"})
	var server *speedtest.Server
	var err error
	if id != "" {
		server, err = client.FetchServerByIDContext(ctx, id)
	} else {
		var servers speedtest.Servers
		servers, err = client.FetchServerListContext(ctx)
		if err == nil {
			server, err = lowestNearbyServer(servers)
		}
	}
	if ctx.Err() != nil {
		return SpeedResult{}, ctx.Err()
	}
	if err != nil {
		return SpeedResult{}, fmt.Errorf("speed-test server discovery failed")
	}
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	progress(SpeedUpdate{Action: "latency", Text: "Latency · 10%"})
	if err = server.PingTestContext(ctx, nil); err != nil {
		return SpeedResult{}, fmt.Errorf("latency test failed")
	}
	result := SpeedResult{Server: server.ID, LatencyMS: float64(server.Latency) / float64(time.Millisecond)}
	progress(SpeedUpdate{Action: "latency", Text: speedResultText("latency", result), Complete: true})
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	progress(SpeedUpdate{Action: "download", Text: "Download · 30%"})
	if err = server.DownloadTestContext(ctx); err != nil {
		return SpeedResult{}, fmt.Errorf("download test failed")
	}
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	result.DownloadMbps = server.DLSpeed.Mbps()
	progress(SpeedUpdate{Action: "download", Text: speedResultText("download", result), Complete: true})
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	progress(SpeedUpdate{Action: "upload", Text: "Upload · 65%"})
	if err = server.UploadTestContext(ctx); err != nil {
		return SpeedResult{}, fmt.Errorf("upload test failed")
	}
	if err := ctx.Err(); err != nil {
		return SpeedResult{}, err
	}
	result.UploadMbps = server.ULSpeed.Mbps()
	result.At = time.Now().UTC().Format(time.RFC3339)
	progress(SpeedUpdate{Action: "upload", Text: speedResultText("upload", result), Complete: true})
	progress(SpeedUpdate{Action: "run", Text: "Complete · 100%"})
	return result, ctx.Err()
}

// Initialize internet speed test state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}, speed: RunSpeedTest, discoverServers: discoverSpeedServers}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	return e
}

// Apply completed internet speed test work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if result.ID == "speed-session" {
		e.drainSpeedProgress()
	}

	text := result.Text
	if result.Err != nil {
		text = "Unavailable\n" + result.Err.Error()
	}
	e.ResultsText[result.ID] = text
	delete(e.Results, result.ID)

	if result.ID == "speed-session" {
		if value, ok := result.Value.(SpeedResult); ok && result.Err == nil {
			v := e.state("speed-history", nil)
			v.SpeedResults = append(v.SpeedResults, value)
			if len(v.SpeedResults) > 20 {
				v.SpeedResults = append([]SpeedResult(nil), v.SpeedResults[len(v.SpeedResults)-20:]...)
			}
			if err := e.routinePersist(); err != nil {
				return err
			}
			for _, a := range []string{"latency", "download", "upload"} {
				e.speedDisplay[a] = speedResultText(a, value)
			}
			e.speedAction = ""
		}
		e.finishSpeed(text)
	}

	destination := result.ID
	if result.ID == "speed-session" {
		destination = e.speedContext
	}

	if err := engine.SendDiagnostic(ctx, c, destination, e.Bindings[destination].Action, result.Value); err != nil {
		return err
	}

	if _, ok := e.Bindings[result.ID]; ok {
		if err := e.Render(ctx, c, result.ID); err != nil {
			return err
		}
	}
	if result.ID == "speed-session" {
		return e.renderSpeed(ctx, c)
	}
	return nil
}

// Build internet speed test status from the current asynchronous result
func (e *Plugin) asyncStatus(id string) displayStatus {
	return engine.AsyncStatus(e.ResultsText[id])
}

// Cancel the active speed measurement and release its ownership
func (e *Plugin) cancelSpeed() {
	e.drainSpeedProgress()
	if _, busy := e.Jobs["speed-session"]; busy {
		e.CancelJob("speed-session")
		e.finishSpeed("Cancelled")
	}
}

// Build the current internet speed test status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	return e.extensionStatus(id, b)

}

// Apply queued speed progress updates on the owner loop
func (e *Plugin) drainSpeedProgress() {
	for {
		select {
		case phase := <-e.progress:
			if job, busy := e.Jobs["speed-session"]; !busy || phase.Generation != job.Generation {
				continue
			}
			update := phase.Update
			switch update.Action {
			case "run", "latency", "download", "upload":
				e.speedDisplay[update.Action] = update.Text
				if update.Complete {
					e.speedAction = "run"
				} else {
					e.speedAction = update.Action
					e.speedDisplay["run"] = update.Text
				}
			}
		default:
			return
		}
	}
}

// Handle internet speed test lifecycle events outside the common action path
func (e *Plugin) extensionEvent(ctx context.Context, c *sdk.Client, ev sdk.Event, _ binding) (bool, error) {

	id := ev.Context
	a := action(ev.Action)
	if ev.Event == "desktopInputResult" {
		var p map[string]any
		_ = json.Unmarshal(ev.Payload, &p)
		if message, ok := p["outcome"].(string); ok {
			e.ResultsText[id] = message
		}
		return true, e.Render(ctx, c, id)
	}

	if !active(ev) && ev.Event != "dialRotate" {
		return true, nil
	}
	{

		switch a {
		case "cancel":
			e.cancelSpeed()
			e.ResultsText[id] = "Cancelled"
		case "run":
			if e.speedDisplay == nil {
				e.speedDisplay = map[string]string{}
			}
			if e.progress == nil {
				e.progress = make(chan speedProgress, 16)
			}
			server, err := e.selectedServer()
			if err != nil {
				return true, err
			}
			generation := e.Generation + 1
			if e.startJob(ctx, "speed-session", func(job context.Context) (string, any, error) {
				result, err := e.speed(job, server, func(update SpeedUpdate) {
					select {
					case e.progress <- speedProgress{Generation: generation, Update: update}:
						select {
						case e.WakeChannel <- struct{}{}:
						default:
						}
					case <-job.Done():
					}
				})
				return speedResultText("run", result), result, err
			}) {
				e.speedContext = id
				e.speedAction = "run"
				e.speedDisplay["run"] = "Working…"
			} else if _, busy := e.Jobs["speed-session"]; !busy {
				e.speedDisplay["run"] = e.ResultsText["speed-session"]
			}
		}
		return true, e.renderSpeed(ctx, c)
	}

}

// Route internet speed test inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op        string         `json:"op"`
		RequestID string         `json:"request_id"`
		Settings  map[string]any `json:"settings"`
		Scope     string         `json:"scope"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Op == "cancel_speed_servers" {
		e.discovery.Cancel(ev.Context, "speed_servers", p.RequestID)
		return true, nil
	}
	if p.Op == "speed_servers" {
		return true, e.discoverSpeedServers(ctx, c, ev)
	}
	if p.Op != "discover" && p.Op != "test" {
		return false, nil
	}
	a := action(ev.Action)
	if a == "" {
		a = "refresh"
	}
	if p.Op == "discover" {

		return true, engine.SendDiagnostic(ctx, c, ev.Context, ev.Action, map[string]any{"history": e.lookupState("speed-history", nil).SpeedResults})

	}

	return true, engine.SendDiagnostic(ctx, c, ev.Context, ev.Action, map[string]any{"message": "Activate a configured action to start its session"})
}

// Process due internet speed test extension work on the owner loop
func (e *Plugin) extensionPoll(ctx context.Context, c *sdk.Client) (bool, error) {

	for id := range e.Bindings {

		if err := e.Render(ctx, c, id); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Select the internet speed test display state for extension-owned work
func (e *Plugin) extensionStatus(id string, b binding) displayStatus {
	status := e.asyncStatus(id)
	{

		if action(b.Action) == "run" && id != e.speedContext {
			return e.asyncStatus(id)
		}
		if action(b.Action) == "cancel" {
			return e.asyncStatus(id)
		}
		status = e.speedStatus(action(b.Action))
		if _, busy := e.Jobs["speed-session"]; busy && action(b.Action) == "run" {
			status.Icon = "icons/busy"
		}
	}

	if _, ok := e.Jobs[id]; ok {
		status.Icon = "icons/busy"
	}
	return status
}

// Record the final speed-test message and settle running state
func (e *Plugin) finishSpeed(text string) {
	e.speedDisplay["run"] = text
	if e.speedAction != "" && e.speedAction != "run" {
		e.speedDisplay[e.speedAction] = text
	}
	e.speedAction = ""
}

// Dispatch an activating internet speed test input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	_, err := e.extensionEvent(ctx, c, ev, b)
	return err
}

// Expose the internet speed test feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Clear: e.discovery.Clear, Close: e.discovery.Close, Action: e.handleAction, Inspector: e.extensionInspector, Settings: e.settingsFeature, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature, Progress: e.drainSpeedProgress}
}

// Find the saved internet speed test state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
}

// Choose the next explicit internet speed test deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	return e.PendingWrite

}

// Run due internet speed test feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	return e.extensionPoll(ctx, c)
}

// Refresh displays affected by the shared speed measurement
func (e *Plugin) renderSpeed(ctx context.Context, c *sdk.Client) error {
	for id := range e.Bindings {
		if err := e.Render(ctx, c, id); err != nil {
			return err
		}
	}
	return nil
}

// Flush changed internet speed test state through the engine persistence policy
func (e *Plugin) routinePersist() error { return engine.RoutinePersist(e.Engine) }

// Apply internet speed test settings-change invalidation
func (e *Plugin) settingsFeature(id string) {
	if id == "" || id == e.speedContext {
		e.cancelSpeed()
	}
}

// Build a display status for the selected speed metric
func (e *Plugin) speedStatus(a string) displayStatus {
	text := e.speedDisplay[a]
	if text == "" {
		if values := e.lookupState("speed-history", nil).SpeedResults; len(values) > 0 && a != "run" {
			text = speedResultText(a, values[len(values)-1])
		}
	}
	if text == "" {
		text = "Ready"
	}
	return displayStatus{Text: text, Unavailable: len(text) > 11 && text[:11] == "Unavailable"}
}

// Schedule bounded internet speed test work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Resolve or initialize the internet speed test state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)

	return v
}

// Format the chosen speed metric from a completed result
func speedResultText(action string, result SpeedResult) string {
	switch action {
	case "latency":
		return fmt.Sprintf("%.1f ms", result.LatencyMS)
	case "download":
		return fmt.Sprintf("↓ %.1f Mbps", result.DownloadMbps)
	case "upload":
		return fmt.Sprintf("↑ %.1f Mbps", result.UploadMbps)
	case "run":
		return fmt.Sprintf("%.1f ms\n↓ %.1f ↑ %.1f Mbps", result.LatencyMS, result.DownloadMbps, result.UploadMbps)
	}
	return ""
}
