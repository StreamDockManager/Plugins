package batterypower

import (
	"context"
	"github.com/StreamDockManager/Plugins/internal/native/observation"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/native/battery"
)

type Plugin struct {
	*engine.Engine
	telemetryExpiry time.Time
	readStatus      func(context.Context, statusRequest) probeStatus
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type probeStatus struct {
	Status displayStatus
}

type sampledStatus = engine.SampledStatus

type statusRequest struct{ binding binding }

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

// Attach battery power lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize battery power state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base}
	return e
}

// Apply completed battery power work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if strings.HasPrefix(result.ID, "status:") {
		return e.applyStatus(ctx, c, result, job.Refresh)
	}
	return e.FinishJob(ctx, c, result, job)
}

// Update shared battery power samples and refresh their bound displays
func (e *Plugin) applyStatus(ctx context.Context, c *sdk.Client, result jobResult, refresh bool) error {
	value, ok := result.Value.(probeStatus)
	if !ok {
		return nil
	}
	now := time.Now()
	// Update current sample owners while retaining refresh and failure-backoff policy
	return engine.CompleteSample(e.Engine, result.ID, func(id string, b binding, previous sampledStatus) sampledStatus {
		interval := time.Second

		configured := engine.RefreshInterval("battery-power", action(b.Action), b.Settings, interval)
		interval = configured
		failures := 0
		if value.Status.Unavailable {
			failures = min(previous.Failures+1, 6)
			interval = max(configured, min(time.Duration(1<<failures)*time.Second, 60*time.Second))
		}
		if refresh {
			interval = 0
		}
		next := now.Add(interval)
		if configured == 0 && !refresh {
			next = engine.RefreshNext(now, 0)
		}
		return sampledStatus{Status: value.Status, At: now, Failures: failures, Next: next}
	}, func(id string, status displayStatus) error { return e.SendDisplay(ctx, c, id, status) }, nil)
}

// Reuse a current battery power sample or schedule a bounded refresh
func (e *Plugin) collectStatus(ctx context.Context, id string, b binding) displayStatus {

	previous := e.Statuses[id]
	key := e.ProbeKey(id, b)
	// Snapshot binding settings before handing work to a background probe
	return engine.CollectSample(e.Engine, id, key, previous, time.Now, func() bool {

		request := statusRequest{binding: b}
		request.binding.Settings = cloneSettings(b.Settings)

		read := e.readStatus
		if read == nil {
			read = func(ctx context.Context, request statusRequest) probeStatus { return request.sample(ctx) }
		}
		return e.startJob(ctx, key, func(ctx context.Context) (string, any, error) {
			value := read(ctx, request)
			return "", value, nil
		})
	}, engine.StaleSample)
}

// Build the current battery power status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("battery-power", action(b.Action), text, err)
}

// Route battery power inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating battery power input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	if !active(ev) && ev.Event != "dialRotate" {
		return nil
	}

	if ev.Event == "dialRotate" {
		delete(e.Effective, ev.Context)

		return nil

	}
	if !active(ev) {
		return nil
	}

	return e.Render(observation.Refresh(ctx), c, ev.Context)
}

// Expose the battery power feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Collect: e.collectStatus}
}

// Choose the next explicit battery power deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	if len(e.Bindings) == 0 {
		e.telemetryExpiry = time.Time{}
		return e.PendingWrite
	}

	if e.NextPeriodic.IsZero() {
		e.NextPeriodic = now.Add(time.Second)
	}
	var deadline time.Time
	earlier := func(at time.Time) {
		if !at.IsZero() && (deadline.IsZero() || at.Before(deadline)) {
			deadline = at
		}
	}
	for id, b := range e.Bindings {
		{
			{

				key := e.ProbeKey(id, b)
				if _, busy := e.Jobs[key]; !busy {
					if status, ok := e.Statuses[id]; ok {
						earlier(status.Next)
					} else {
						earlier(now)
					}
				}
			}
		}

	}

	earlier(e.PendingWrite)
	return deadline
}

// Read a detached battery power status request away from binding mutation
func (r statusRequest) sample(ctx context.Context) probeStatus {
	sample := Plugin{}
	status := sample.displayStatus(ctx, "", r.binding)
	return probeStatus{Status: status}
}

// Schedule bounded battery power work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Compute the battery power text shown for the selected action
func (e *Plugin) text(ctx context.Context, _ string, _ binding) (string, error) {

	return battery.Battery(ctx)

}
