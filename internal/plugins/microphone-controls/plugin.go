package microphonecontrols

import (
	"context"
	"encoding/json"
	discovery "github.com/StreamDockManager/Plugin-SDK/Go/discovery"
	"path/filepath"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

type Plugin struct {
	discovery discovery.Runner
	*engine.Engine
	telemetryExpiry time.Time
	readStatus      func(context.Context, statusRequest) probeStatus
	mic             *microphone.Controller
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type probeStatus struct {
	Status displayStatus
}

type sampledStatus = engine.SampledStatus

type statusRequest struct {
	binding binding
	mic     *microphone.Controller
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

// Attach microphone controls lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize microphone controls state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base}
	return e
}

// Apply completed microphone controls work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if strings.HasPrefix(result.ID, "status:") {
		return e.applyStatus(ctx, c, result, job.Refresh)
	}
	return e.FinishJob(ctx, c, result, job)
}

// Update shared microphone controls samples and refresh their bound displays
func (e *Plugin) applyStatus(ctx context.Context, c *sdk.Client, result jobResult, refresh bool) error {
	value, ok := result.Value.(probeStatus)
	if !ok {
		return nil
	}
	now := time.Now()
	// Update current sample owners while retaining refresh and failure-backoff policy
	return engine.CompleteSample(e.Engine, result.ID, func(id string, b binding, previous sampledStatus) sampledStatus {
		interval := time.Second

		configured := engine.RefreshInterval("microphone-controls", action(b.Action), b.Settings, interval)
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

// Reuse a current microphone controls sample or schedule a bounded refresh
func (e *Plugin) collectStatus(ctx context.Context, id string, b binding) displayStatus {

	previous := e.Statuses[id]
	key := e.ProbeKey(id, b)
	// Snapshot binding settings before handing work to a background probe
	return engine.CollectSample(e.Engine, id, key, previous, time.Now, func() bool {
		{

			e.microphoneController()
		}

		request := statusRequest{binding: b, mic: e.mic}
		request.binding.Settings = cloneSettings(b.Settings)

		read := e.readStatus
		if read == nil {
			read = func(ctx context.Context, request statusRequest) probeStatus { return request.sample(ctx) }
		}
		return engine.StartObservationJob(ctx, e.Engine, key, func(ctx context.Context) (string, any, error) {
			value := read(ctx, request)
			return "", value, nil
		}, e.applyResult)
	}, engine.StaleSample)
}

// Build the current microphone controls status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	s := b.Settings
	{

		d, err := e.microphoneController().Resolve(ctx, configvalue.String(s, "device", "default"))
		if err != nil {
			return displayStatus{Text: "Unavailable\n" + err.Error(), Unavailable: true}
		}
		state, label := 0, "Unmuted"
		if d.Muted {
			state, label = 1, "Muted"
		}

		return displayStatus{Text: d.Name + "\n" + label + "\n" + d.Mode, State: state}
	}

}

// Route microphone controls inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op        string         `json:"op"`
		RequestID string         `json:"request_id"`
		Settings  map[string]any `json:"settings"`
		Scope     string         `json:"scope"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Op == "cancel_microphones" {
		e.discovery.Cancel(ev.Context, "microphones", p.RequestID)
		return true, nil
	}
	if p.Op != "discover" && p.Op != "test" && p.Op != "microphones" {
		return false, nil
	}
	a := action(ev.Action)
	if a == "" {
		a = "refresh"
	}
	if p.Op == "discover" || p.Op == "microphones" {
		return true, e.discoverMicrophones(ctx, c, ev)
	}

	return true, c.Send(ctx, sdk.Message("sendToPropertyInspector", ev.Context, map[string]any{"message": "Activate a configured action to start its session"}))
}

// Dispatch an activating microphone controls input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	s := b.Settings
	if !active(ev) && ev.Event != "dialRotate" {
		return nil
	}
	a := action(ev.Action)

	if ev.Event == "dialRotate" {
		delete(e.Effective, ev.Context)

		return nil

	}
	if !active(ev) {
		return nil
	}
	{

		_, err := e.microphoneController().Apply(ctx, configvalue.String(s, "device", "default"), a)
		if err != nil {
			return err
		}
	}

	return e.Render(ctx, c, ev.Context)
}

// Expose the microphone controls feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Clear: e.discovery.Clear, Close: e.discovery.Close, Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Collect: e.collectStatus}
}

// Obtain the injected or platform microphone controller
func (e *Plugin) microphoneController() *microphone.Controller {
	if e.mic == nil {
		e.mic = &microphone.Controller{Adapter: microphone.Native(filepath.Join(filepath.Dir(e.Path), "microphone-gains.json"))}
	}
	return e.mic
}

// Choose the next explicit microphone controls deadline without idle polling
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
		if !e.WantsLiveView(id) {
			continue
		}
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

// Read a detached microphone controls status request away from binding mutation
func (r statusRequest) sample(ctx context.Context) probeStatus {
	sample := Plugin{mic: r.mic}
	status := sample.displayStatus(ctx, "", r.binding)
	return probeStatus{Status: status}
}
