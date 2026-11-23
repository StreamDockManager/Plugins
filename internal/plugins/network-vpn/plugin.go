package networkvpn

import (
	"context"
	"github.com/StreamDockManager/Plugins/internal/native/observation"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/native/vpn"
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

// Attach network vpn lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize network vpn state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base}
	return e
}

// Apply completed network vpn work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if strings.HasPrefix(result.ID, "status:") {
		return e.applyStatus(ctx, c, result, job.Refresh)
	}
	return e.FinishJob(ctx, c, result, job)
}

// Update shared network vpn samples and refresh their bound displays
func (e *Plugin) applyStatus(ctx context.Context, c *sdk.Client, result jobResult, refresh bool) error {
	value, ok := result.Value.(probeStatus)
	if !ok {
		return nil
	}
	now := e.Time()
	// Update current sample owners while retaining refresh and failure-backoff policy
	return engine.CompleteSample(e.Engine, result.ID, func(id string, b binding, previous sampledStatus) sampledStatus {
		interval, failures := networkCadence(previous, value.Status)
		configured := engine.RefreshInterval("network-vpn", action(b.Action), b.Settings, interval)
		if !value.Status.Unavailable {
			interval = configured
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

// Reuse a current network vpn sample or schedule a bounded refresh
func (e *Plugin) collectStatus(ctx context.Context, id string, b binding) displayStatus {

	previous := e.Statuses[id]
	key := e.ProbeKey(id, b)
	return engine.CollectSample(e.Engine, id, key, previous, e.Time, func() bool {

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

// Build the current network vpn status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("network-vpn", action(b.Action), text, err)
}

// Route network vpn inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating network vpn input to its action implementation
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

// Expose the network vpn feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Collect: e.collectStatus}
}

// Choose the next explicit network vpn deadline without idle polling
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

// Read a detached network vpn status request away from binding mutation
func (r statusRequest) sample(ctx context.Context) probeStatus {
	sample := Plugin{}
	status := sample.displayStatus(ctx, "", r.binding)
	return probeStatus{Status: status}
}

// Schedule bounded network vpn work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Compute the network vpn text shown for the selected action
func (e *Plugin) text(ctx context.Context, _ string, b binding) (string, error) {
	s := b.Settings
	a := action(b.Action)
	{

		switch a {
		case "vpn":
			return vpn.VPN(ctx)
		case "reachability":
			address := configvalue.String(s, "address", "1.1.1.1:443")
			conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", address)
			if err != nil {
				return "Offline", err
			}
			conn = sdk.TrackConn(conn)
			conn.Close()
			return "Reachable", nil
		default:
			interfaces, err := net.Interfaces()
			if err != nil {
				return "", err
			}
			names := []string{}
			for _, i := range interfaces {
				if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
					addresses, _ := i.Addrs()
					parts := []string{}
					for _, address := range addresses {
						parts = append(parts, address.String())
					}
					names = append(names, i.Name+" "+strings.Join(parts, ", "))
				}
			}
			return strings.Join(names, "\n"), nil
		}
	}

}

// Adapt network sampling cadence to change and failure history
func networkCadence(previous sampledStatus, current displayStatus) (time.Duration, int) {
	if current.Unavailable {
		failures := min(previous.Failures+1, 6)
		return min(time.Duration(1<<failures)*time.Second, 60*time.Second), failures
	}
	interval := time.Second
	if !previous.At.IsZero() && previous.Failures == 0 && reflect.DeepEqual(previous.Status, current) {
		interval = min(max(time.Second, previous.Next.Sub(previous.At))*2, 30*time.Second)
	}
	return interval, 0
}
