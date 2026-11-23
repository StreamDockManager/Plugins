package httpwebhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/httpclient"
	sharedpoll "github.com/StreamDockManager/Plugin-SDK/Go/polling"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/pollingdisplay"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Attach HTTP webhook lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize HTTP webhook state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, inspectors: map[string]bool{}, explicit: map[string]bool{}, httpCache: newHTTPValidatorCache(), polling: map[string]pollingState{}}
	return e
}

// Attach HTTP webhook polling metadata to a completed job
func (e *Plugin) annotatePolling(id string, result jobResult) any {
	info := pollingdisplay.PollingFeedback(e.polling[id], result.Err)
	switch value := result.Value.(type) {
	case HTTPResult:
		value.PollingFeedback = info
		return value

	}
	return result.Value
}

// Apply completed HTTP webhook work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {

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

	if e.preview(result.ID) {
		delete(e.explicit, result.ID)
		if err := engine.SendDiagnostic(ctx, c, result.ID, e.Bindings[result.ID].Action, result.Value); err != nil {
			return err
		}
	}

	if _, ok := e.Bindings[result.ID]; ok {
		if err := e.Render(ctx, c, result.ID); err != nil {
			return err
		}
	}

	return nil
}

// Build HTTP webhook status from the current asynchronous result
func (e *Plugin) asyncStatus(id string) displayStatus {
	return engine.AsyncStatus(e.ResultsText[id])
}

// Track the requested refresh interval for a HTTP webhook binding
func (e *Plugin) attachPolling(id string, interval time.Duration) {
	pollingdisplay.Attach(e.polling, e.Jobs, id, interval)
}

// Release transient HTTP webhook state belonging to one binding
func (e *Plugin) clearFeature(id string) { delete(e.polling, id); delete(e.explicit, id) }

// Clear HTTP webhook transient resources during feature shutdown
func (e *Plugin) closeFeature() { e.httpCache.Reset(); clear(e.polling) }

// Accept a current HTTP webhook poll result and calculate its next attempt
func (e *Plugin) completePolling(id string, job pollingJob, result jobResult, now time.Time, jitter float64) (text string, retain bool) {
	state := e.polling[id]
	failed := result.Err != nil
	var retry time.Time
	switch value := result.Value.(type) {
	case HTTPResult:
		failed = failed || value.Status >= 400 || (value.Status == 304 && !value.Revalidated)
		retry = value.RetryAfter

	}
	reason := ""
	if value, ok := result.Value.(HTTPResult); ok && value.Status > 0 {
		reason = "HTTP " + strconv.Itoa(value.Status)
	}
	text, retain = pollingdisplay.CompletePolling(&state, e.pollingValue(id, result), result.Text, reason, failed, now, job.Interval, jitter, retry)
	e.polling[id] = state
	e.Checked[id] = now
	return text, retain
}

// Delay a HTTP webhook poll that could not enter the work queue
func (e *Plugin) deferRejectedPoll(id string) {
	pollingdisplay.DeferRejected(e.polling, e.Jobs, id, time.Now())
}

// Build the current HTTP webhook status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	return e.extensionStatus(id, b)

}

// Handle HTTP webhook lifecycle events outside the common action path
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

		if a == "cancel" {
			e.CancelJob(id)
			e.ResultsText[id] = "Cancelled"
		} else {
			e.startRequest(ctx, c, id, b.Action, s, false)
		}
	}

	return true, e.Render(ctx, c, id)
}

// Route HTTP webhook inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op       string         `json:"op"`
		Settings map[string]any `json:"settings"`
		Scope    string         `json:"scope"`
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

		return true, engine.SendDiagnostic(ctx, c, ev.Context, ev.Action, map[string]any{"message": "This action has no runtime discovery"})

	}

	{

		e.explicit[ev.Context] = true
		e.startRequest(ctx, c, ev.Context, ev.Action, s, false)
		return true, nil
	}

}

// Process due HTTP webhook extension work on the owner loop
func (e *Plugin) extensionPoll(ctx context.Context, c *sdk.Client) (bool, error) {

	for id, b := range e.Bindings {
		s := e.MergedSettings(b.Settings)
		a := action(b.Action)
		{

			if e.automaticRead(id, a, s) && e.pollingDue(id, time.Now()) {
				e.startRequest(ctx, c, id, b.Action, s, true)
			}
		}

		if err := e.Render(ctx, c, id); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Select the HTTP webhook display state for extension-owned work
func (e *Plugin) extensionStatus(id string, b binding) displayStatus {
	status := e.asyncStatus(id)

	{

		if result, ok := e.Results[id].(HTTPResult); ok && !e.polling[id].Stale && e.polling[id].Failures == 0 {
			switch action(b.Action) {
			case "status":
				status.Text = fmt.Sprintf("HTTP %d", result.Status)
				if !status.Unavailable {
					status.Content = &engine.Presentation{Kind: "value", Primary: strconv.Itoa(result.Status), Label: "Status"}
				}
			case "duration":
				status.Text = fmt.Sprintf("%d ms", result.DurationMS)
				if !status.Unavailable {
					status.Content = &engine.Presentation{Kind: "value", Primary: status.Text, Label: "Duration"}
				}
			}
		}
	}

	if _, ok := e.Jobs[id]; ok {
		status.Icon = "icons/busy"
	}
	return status
}

// Dispatch an activating HTTP webhook input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	_, err := e.extensionEvent(ctx, c, ev, b)
	return err
}

// Expose the HTTP webhook feature callbacks used by the shared engine
func (e *Plugin) preview(id string) bool { return !e.lifecycle || e.inspectors[id] || e.explicit[id] }

func (e *Plugin) observeInspector(_ context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	if !e.hostChecked && c != nil {
		var info struct {
			Lifecycle bool `json:"inspector_lifecycle"`
		}
		_ = json.Unmarshal(c.Host().Raw["streamDockManager"], &info)
		e.lifecycle, e.hostChecked = info.Lifecycle, true
	}
	switch ev.Event {
	case "streamDockManagerPermissionsChanged":
		for id, b := range e.Bindings {
			if configvalue.Bool(e.MergedSettings(b.Settings), "use_vault_webhook") {
				e.CancelJob(id)
				delete(e.Results, id)
				delete(e.ResultsText, id)
				delete(e.Last, id)
				delete(e.polling, id)
			}
		}
	case "propertyInspectorDidAppear":
		e.inspectors[ev.Context] = true
	case "propertyInspectorDidDisappear":
		delete(e.inspectors, ev.Context)
		delete(e.explicit, ev.Context)
	}
	return false, nil
}

func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Before: e.observeInspector, Action: e.handleAction, Inspector: e.extensionInspector, Settings: e.settingsFeature, Clear: e.clearFeature, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature}
}

// Retain useful HTTP webhook output while an automatic refresh is pending
func (e *Plugin) keepPollingDisplay(id string, automatic bool, previousText string) {
	pollingdisplay.KeepDisplay(e.polling, e.ResultsText, id, automatic, previousText)
}

// Choose the next explicit HTTP webhook deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {
	return pollingdisplay.Deadline(e.Engine, e.polling, now, e.automaticRead)
}

// Run due HTTP webhook feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	return e.extensionPoll(ctx, c)
}

// Check whether a HTTP webhook binding permits automatic read polling
func (e *Plugin) automaticRead(id, action string, settings map[string]any) bool {
	if engine.RefreshPolicy("http-webhooks", action) == nil {
		return false
	}
	method := strings.ToUpper(configvalue.String(settings, "method", "GET"))
	if method != "GET" && method != "HEAD" {
		return false
	}
	if pollingInterval("http-webhooks", action, settings) > 0 {
		return true
	}
	// Only a valid, explicit Off selection loads once. Missing/invalid modern
	// configuration never authorizes automatic network requests
	if _, explicit := settings["update_interval_ms"]; !explicit {
		return false
	}
	interval, err := engine.RefreshPolicy("http-webhooks", action).Resolve(settings)
	return err == nil && interval == 0 && !e.polling[id].Next.Equal(sharedpoll.Never)
}

// Check whether the HTTP webhook binding refresh deadline has elapsed
func (e *Plugin) pollingDue(id string, now time.Time) bool {
	return pollingdisplay.Due(e.polling, e.Jobs, id, now)
}

// Extract display text from the completed HTTP webhook poll
func (e *Plugin) pollingValue(id string, result jobResult) string {
	switch value := result.Value.(type) {
	case HTTPResult:
		switch action(e.Bindings[id].Action) {
		case "status":
			return fmt.Sprintf("HTTP %d", value.Status)
		case "duration":
			return fmt.Sprintf("%d ms", value.DurationMS)
		}
		if _, last, found := strings.Cut(result.Text, "\n"); found {
			return last
		}

	}
	return result.Text
}

// Apply HTTP webhook settings-change invalidation
func (e *Plugin) settingsFeature(id string) {
	if id != "" {
		// Request identity already partitions validators by resolved credentials,
		// endpoint, payload and extraction. Preserve shared identities and peers
		// Engine.ClearContext retires this binding's in-flight generation first
		delete(e.polling, id)
		return
	}
	e.httpCache.Reset()
	clear(e.polling)
	clear(e.Results)
	clear(e.ResultsText)
}

// Schedule an explicit HTTP request for the selected binding
func (e *Plugin) startHTTP(ctx context.Context, id string, s map[string]any) {
	e.startHTTPPolling(ctx, id, s, false)
}

// Schedule an HTTP request with explicit or automatic polling ownership
func (e *Plugin) startHTTPPolling(ctx context.Context, id string, s map[string]any, automatic bool) {
	e.startRequest(ctx, nil, id, e.Bindings[id].Action, s, automatic)
}

func (e *Plugin) startRequest(ctx context.Context, client *sdk.Client, id, uuid string, s map[string]any, automatic bool) {
	s = cloneSettings(s)
	cache := e.httpCache
	previousText := e.ResultsText[id]
	options := httpRequestOptions{Cache: cache, Automatic: automatic}
	fetch := func(job context.Context, settings map[string]any) (HTTPResult, error) {
		return requestHTTP(job, settings, options)
	}
	if display, ok := any(options).(interface {
		DisplayRequest(context.Context, map[string]any) (HTTPResult, error)
	}); ok && !e.preview(id) {
		fetch = display.DisplayRequest
	}
	started := e.startJob(ctx, id, func(job context.Context) (string, any, error) {
		if configvalue.Bool(s, "use_vault_webhook") {
			result, err := vaultWebhook(job, client, uuid, s, automatic)
			if err != nil {
				return "", result, err
			}
			return fmt.Sprintf("HTTP %d · %d ms", result.Status, result.DurationMS), result, nil
		}
		result, err := fetch(job, s)
		if err != nil {
			return "", result, err
		}
		text := fmt.Sprintf("HTTP %d · %d ms", result.Status, result.DurationMS)
		field := configvalue.String(s, "field", "")
		if field != "" {
			value, ok := result.Fields[field]
			if ok {
				text += "\n" + resultText(value)
			} else {
				text += "\nMissing: " + field
			}
		}
		return text, result, nil
	})
	if started {
		e.keepPollingDisplay(id, automatic, previousText)
		e.attachPolling(id, pollingInterval("http-webhooks", "request", s))
	} else {
		e.deferRejectedPoll(id)
	}
}

// Schedule bounded HTTP webhook work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

type HTTPResult = httpclient.HTTPResult

type Plugin struct {
	inspectors, explicit   map[string]bool
	lifecycle, hostChecked bool
	*engine.Engine
	polling   map[string]pollingState
	httpCache *httpValidatorCache
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type httpRequestOptions = httpclient.Options

type httpValidatorCache = httpclient.ValidatorCache

type jobResult = engine.JobResult

type pollingJob = pollingdisplay.Job

type pollingState = sharedpoll.PollState

var RequestHTTP = httpclient.RequestHTTP

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

const maximumRetryAfter = time.Duration(math.MaxInt64)

var newHTTPValidatorCache = httpclient.NewValidatorCache

var parseRetryAfter = httpclient.ParseRetryAfter

var requestHTTP = httpclient.Request

var resultText = engine.ResultText
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

var retryDelay = httpclient.RetryDelay

const maximumPollBackoff = httpclient.MaximumPollBackoff

func vaultWebhook(ctx context.Context, client *sdk.Client, uuid string, s map[string]any, automatic bool) (HTTPResult, error) {
	name := action(uuid)
	if uuid != engine.ID(Definition.Slug)+"."+name {
		return HTTPResult{}, fmt.Errorf("Choose a webhook for this action")
	}
	value, err := metadata.URLCredential(ctx, client, Definition.Slug, uuid, name, "webhook")
	if err != nil {
		return HTTPResult{}, err
	}
	return httpclient.Request(ctx, s, httpclient.Options{VaultURL: value, Automatic: automatic})
}
