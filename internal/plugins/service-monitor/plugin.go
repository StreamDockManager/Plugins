package servicemonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/httpclient"
	sharedpoll "github.com/StreamDockManager/Plugin-SDK/Go/polling"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	process "github.com/StreamDockManager/Plugins/internal/native/command"
	"github.com/StreamDockManager/Plugins/internal/pollingdisplay"
)

// Attach service monitor lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Check the configured service using its selected probe type
func checkService(ctx context.Context, kind string, s map[string]any, cache *httpValidatorCache, automatic bool) (string, any, error) {
	seconds := configvalue.Number(s, "timeout", 5)
	if seconds <= 0 || seconds > 120 {
		return "", nil, fmt.Errorf("timeout must be 0–120 seconds")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds*float64(time.Second)))
	defer cancel()
	start := time.Now()
	available := false
	var err error
	data := serviceResult{}
	switch kind {
	case "http":
		settings := cloneSettings(s)
		settings["method"] = "GET"
		settings["body"] = ""
		settings["timeout"] = seconds
		delete(settings, "fields")
		delete(settings, "field")
		var response HTTPResult
		response, err = requestHTTP(ctx, settings, httpRequestOptions{Cache: cache, Automatic: automatic, StatusOnly: true, NoRedirect: true, AllowUserInfo: true})
		available = response.Status >= 200 && response.Status < 400 && (response.Status != 304 || response.Revalidated)
		data.HTTPStatus = response.Status
		data.Revalidated = response.Revalidated
		data.RetryAfter = response.RetryAfter
		if response.Status >= 100 && response.Status <= 599 {
			err = nil
			if !available {
				data.Reason = fmt.Sprintf("HTTP %d", response.Status)
			}
		}
	case "tcp":
		var conn net.Conn
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", configvalue.String(s, "address", ""))
		if err == nil {
			conn = sdk.TrackConn(conn)
			available = true
			conn.Close()
		}
	case "service":
		name := configvalue.String(s, "service", "")
		if !serviceName.MatchString(name) {
			return "", nil, fmt.Errorf("invalid local service name")
		}
		var text string
		switch runtime.GOOS {
		case "windows":
			text, err = helper(ctx, "sc.exe", "query", name)
			available, data.Reason, err = localServiceObservation("windows", name, text, err)
		case "darwin":
			text, err = helper(ctx, "launchctl", "list")
			available, data.Reason, err = localServiceObservation("darwin", name, text, err)
		default:
			text, err = helper(ctx, "systemctl", "is-active", "--", name)
			available, data.Reason, err = localServiceObservation("linux", name, text, err)
		}
	default:
		return "", nil, fmt.Errorf("unknown service check")
	}
	latency := time.Since(start).Milliseconds()
	label := "Down"
	if available {
		label = "Available"
	}
	if err != nil {
		label = "Unknown"
		data.Unknown = true
		data.Reason = err.Error()
	}
	at := time.Now().UTC().Format(time.RFC3339)
	data.Available = available
	data.LastCheck = at
	if kind != "service" {
		data.LatencyMS = &latency
		if data.Reason != "" {
			label += " · " + data.Reason
		}
		return fmt.Sprintf("%s · %d ms\n%s", label, latency, at), data, err
	}
	if data.Reason != "" {
		label += " · " + data.Reason
	}
	return label + "\n" + at, data, err
}

// Run a bounded local service helper command
func helper(ctx context.Context, name string, args ...string) (string, error) {
	return runHelper(process.BackgroundCommandContext(ctx, name, args...), name)
}

// Initialize service monitor state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, httpCache: newHTTPValidatorCache(), polling: map[string]pollingState{}}
	return e
}

// Capture helper output without exceeding the configured byte limit
func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 1<<20 {
		_, _ = b.Buffer.Write(p[:min(n, (1<<20)-b.Len())])
	}
	return n, nil
}

// Attach service monitor polling metadata to a completed job
func (e *Plugin) annotatePolling(id string, result jobResult) any {
	info := pollingdisplay.PollingFeedback(e.polling[id], result.Err)
	switch value := result.Value.(type) {

	case serviceResult:
		value.PollingFeedback = info
		return value
	}
	return result.Value
}

// Apply completed service monitor work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if choices, ok := result.Value.([]serviceChoice); ok {
		payload := map[string]any{"op": "local_services", "devices": choices}
		if result.Err != nil {
			payload["error"] = result.Err.Error()
		}
		result.Value = payload
	}

	text := result.Text
	if result.Err != nil {
		text = "Unavailable\n" + result.Err.Error()
	}
	retain := false
	if poll, ok := job.Poll.(*pollingJob); ok {
		text, retain = e.completePolling(result.ID, *poll, result, time.Now(), pollJitter())
		result.Value = e.annotatePolling(result.ID, result)
	}
	e.ResultsText[result.ID] = text

	if !retain {
		delete(e.Results, result.ID)
	}
	if value, ok := result.Value.(HTTPResult); ok && !retain {
		value.Preview = nil
		value.Paths = nil
		e.Results[result.ID] = value
	}

	destination := result.ID

	if err := engine.SendDiagnostic(ctx, c, destination, result.Value); err != nil {
		return err
	}

	if _, ok := e.Bindings[result.ID]; ok {
		if err := e.Render(ctx, c, result.ID); err != nil {
			return err
		}
	}

	return nil
}

// Build service monitor status from the current asynchronous result
func (e *Plugin) asyncStatus(id string) displayStatus {
	text := e.ResultsText[id]
	if text == "" {
		text = "Ready"
	}
	return displayStatus{Text: text, Unavailable: strings.HasPrefix(text, "Unavailable") || strings.HasPrefix(text, "Stale") || strings.HasPrefix(text, "Down") || strings.HasPrefix(text, "Unknown")}
}

// Track the requested refresh interval for a service monitor binding
func (e *Plugin) attachPolling(id string, interval time.Duration) {
	pollingdisplay.Attach(e.polling, e.Jobs, id, interval)
}

// Release transient service monitor state belonging to one binding
func (e *Plugin) clearFeature(id string) { delete(e.polling, id) }

// Clear service monitor transient resources during feature shutdown
func (e *Plugin) closeFeature() { e.httpCache.Reset(); clear(e.polling) }

// Accept a current service monitor poll result and calculate its next attempt
func (e *Plugin) completePolling(id string, job pollingJob, result jobResult, now time.Time, jitter float64) (text string, retain bool) {
	state := e.polling[id]
	failed := result.Err != nil
	var retry time.Time
	switch value := result.Value.(type) {

	case serviceResult:
		failed = failed || value.Unknown
		retry = value.RetryAfter
	}
	reason := ""
	if value, ok := result.Value.(serviceResult); ok {
		reason = value.Reason
	}
	if value, ok := result.Value.(HTTPResult); ok && value.Status > 0 {
		reason = "HTTP " + strconv.Itoa(value.Status)
	}
	text, retain = pollingdisplay.CompletePolling(&state, e.pollingValue(id, result), result.Text, reason, failed, now, job.Interval, jitter, retry)
	e.polling[id] = state
	e.Checked[id] = now
	return text, retain
}

// Delay a service monitor poll that could not enter the work queue
func (e *Plugin) deferRejectedPoll(id string) {
	pollingdisplay.DeferRejected(e.polling, e.Jobs, id, time.Now())
}

// Build the current service monitor status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	return e.extensionStatus(id, b)

}

// Handle service monitor lifecycle events outside the common action path
func (e *Plugin) extensionEvent(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) (bool, error) {

	id := ev.Context
	a := action(ev.Action)
	s := e.MergedSettings(b.Settings)
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

		if a != "refresh" {
			e.startService(ctx, id, a, s)
		} else {
			for other, bind := range e.Bindings {
				if action(bind.Action) != "refresh" {
					e.startService(ctx, other, action(bind.Action), e.MergedSettings(bind.Settings))
				}
			}
		}
	}

	return true, e.Render(ctx, c, id)
}

// Route service monitor inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op        string         `json:"op"`
		RequestID string         `json:"request_id"`
		Settings  map[string]any `json:"settings"`
		Scope     string         `json:"scope"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Op != "discover" && p.Op != "test" {
		return false, nil
	}
	s := e.MergedSettings(p.Settings)
	a := action(ev.Action)
	if a == "" {
		a = "refresh"
	}
	if p.Op == "discover" {
		if a == "service" {
			if e.startJob(ctx, ev.Context, func(job context.Context) (string, any, error) {
				choices, err := discoverServices(job)
				payload := map[string]any{"op": "local_services", "devices": choices}
				if p.RequestID != "" {
					payload["request_id"] = p.RequestID
				}
				if err != nil {
					payload["error"] = err.Error()
				}
				return "", payload, err
			}) {
				return true, nil
			}
			return true, c.Send(ctx, sdk.Message("sendToPropertyInspector", ev.Context, map[string]any{"op": "local_services", "request_id": p.RequestID, "error": "Service discovery is busy. Try again shortly."}))
		}
		return true, c.Send(ctx, sdk.Message("sendToPropertyInspector", ev.Context, map[string]any{"message": "This action has no runtime discovery"}))

	}

	{

		e.startService(ctx, ev.Context, a, s)
		return true, nil
	}

}

// Process due service monitor extension work on the owner loop
func (e *Plugin) extensionPoll(ctx context.Context, c *sdk.Client) (bool, error) {

	for id, b := range e.Bindings {
		s := e.MergedSettings(b.Settings)
		a := action(b.Action)
		{

			if e.automaticRead(id, a, s) && e.pollingDue(id, time.Now()) {
				e.startServicePolling(ctx, id, a, s, true)
			}
		}

		if err := e.Render(ctx, c, id); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Select the service monitor display state for extension-owned work
func (e *Plugin) extensionStatus(id string, _ binding) displayStatus {
	status := e.asyncStatus(id)

	if _, ok := e.Jobs[id]; ok {
		status.Icon = "icons/busy"
	}
	return status
}

// Dispatch an activating service monitor input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	_, err := e.extensionEvent(ctx, c, ev, b)
	return err
}

// Expose the service monitor feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Settings: e.settingsFeature, Clear: e.clearFeature, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature}
}

// Retain useful service monitor output while an automatic refresh is pending
func (e *Plugin) keepPollingDisplay(id string, automatic bool, previousText string) {
	pollingdisplay.KeepDisplay(e.polling, e.ResultsText, id, automatic, previousText)
}

// Choose the next explicit service monitor deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {
	return pollingdisplay.Deadline(e.Engine, e.polling, now, e.automaticRead)
}

// Run due service monitor feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	return e.extensionPoll(ctx, c)
}

// Check whether a service monitor binding permits automatic read polling
func (e *Plugin) automaticRead(id, action string, settings map[string]any) bool {
	if engine.RefreshPolicy("service-monitor", action) == nil {
		return false
	}
	if pollingInterval("service-monitor", action, settings) > 0 {
		return true
	}
	// Only a valid, explicit Off selection loads once. Missing/invalid modern
	// configuration never authorizes automatic network requests
	if _, explicit := settings["update_interval_ms"]; !explicit {
		return false
	}
	interval, err := engine.RefreshPolicy("service-monitor", action).Resolve(settings)
	return err == nil && interval == 0 && !e.polling[id].Next.Equal(sharedpoll.Never)
}

// Check whether the service monitor binding refresh deadline has elapsed
func (e *Plugin) pollingDue(id string, now time.Time) bool {
	return pollingdisplay.Due(e.polling, e.Jobs, id, now)
}

// Extract display text from the completed service monitor poll
func (e *Plugin) pollingValue(_ string, result jobResult) string {
	switch result.Value.(type) {

	case serviceResult:
		first, _, _ := strings.Cut(result.Text, "\n")
		return first
	}
	return result.Text
}

// Apply service monitor settings-change invalidation
func (e *Plugin) settingsFeature(id string) {
	if id != "" {
		delete(e.polling, id)
		return
	}
	e.httpCache.Reset()
	clear(e.polling)
	clear(e.Results)
	clear(e.ResultsText)
}

// Schedule bounded service monitor work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Schedule an explicit service check for the activating binding
func (e *Plugin) startService(ctx context.Context, id, a string, s map[string]any) {
	e.startServicePolling(ctx, id, a, s, false)
}

// Schedule a service check with explicit or automatic polling ownership
func (e *Plugin) startServicePolling(ctx context.Context, id, a string, s map[string]any, automatic bool) {
	s = cloneSettings(s)
	cache := e.httpCache
	previousText := e.ResultsText[id]
	if e.startJob(ctx, id, func(job context.Context) (string, any, error) { return checkService(job, a, s, cache, automatic) }) {
		e.keepPollingDisplay(id, automatic, previousText)
		e.attachPolling(id, pollingInterval("service-monitor", a, s))
	} else {
		e.deferRejectedPoll(id)
	}
}

// Run the prepared helper command with bounded output handling
func runHelper(cmd *exec.Cmd, name string) (string, error) {
	cmd.WaitDelay = 2 * time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("%s failed; check installation, selection and permissions", name)
	}
	return strings.TrimSpace(out.String()), nil
}

type HTTPResult = httpclient.HTTPResult

type Plugin struct {
	*engine.Engine
	polling   map[string]pollingState
	httpCache *httpValidatorCache
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type boundedOutput struct{ bytes.Buffer }

type displayStatus = engine.DisplayStatus

type httpRequestOptions = httpclient.Options

type httpValidatorCache = httpclient.ValidatorCache

type jobResult = engine.JobResult

type pollingJob = pollingdisplay.Job

type pollingState = sharedpoll.PollState

type serviceResult struct {
	httpclient.PollingFeedback
	Available   bool      `json:"available"`
	Unknown     bool      `json:"unknown,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	LastCheck   string    `json:"last_check"`
	LatencyMS   *int64    `json:"latency_ms,omitempty"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	Revalidated bool      `json:"revalidated,omitempty"`
	RetryAfter  time.Time `json:"-"`
}

var RequestHTTP = httpclient.RequestHTTP

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

var newHTTPValidatorCache = httpclient.NewValidatorCache

var requestHTTP = httpclient.Request

var serviceName = regexp.MustCompile(`^[a-zA-Z0-9_.@-]{1,160}$`)
var pollJitter = httpclient.PollJitter

// Resolve the configured refresh cadence for the selected action
func pollingInterval(slug, action string, settings map[string]any) time.Duration {
	if action == "cancel" || (slug == "service-monitor" && action == "refresh") {
		return 0
	}
	method := "GET"
	if slug == "http-webhooks" {
		method = strings.ToUpper(configvalue.String(settings, "method", "GET"))
	}
	if method != "GET" && method != "HEAD" {
		return 0
	}
	policy := engine.RefreshPolicy(slug, action)
	if policy == nil {
		return 0
	}
	if _, explicit := settings["update_interval_ms"]; !explicit {
		return 0
	}
	interval, err := policy.Resolve(settings)
	if err != nil {
		return 0
	}
	return interval
}
