package systemmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	netstat "github.com/shirou/gopsutil/v4/net"
)

type Plugin struct {
	*engine.Engine
	sharedTelemetry       *sdk.TelemetrySnapshot
	telemetrySubscription string
	telemetryExpiry       time.Time
	readStatus            func(context.Context, statusRequest) probeStatus
	lastNetwork           uint64
	lastNetworkTime       time.Time
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type probeStatus struct {
	Status      displayStatus
	Network     uint64
	NetworkTime time.Time
}

type sampledStatus = engine.SampledStatus

type statusRequest struct {
	binding     binding
	network     uint64
	networkTime time.Time
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

// Attach system monitor lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize system monitor state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base}
	return e
}

// Apply system monitor follow-up work after the common event handler
func (e *Plugin) afterEvent(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	if ev.Event == "willAppear" || ev.Event == "willDisappear" || ev.Event == "didReceiveSettings" {
		return e.subscribeTelemetry(ctx, c)
	}
	return nil
}

// Apply completed system monitor work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if strings.HasPrefix(result.ID, "status:") {
		return e.applyStatus(ctx, c, result, job.Refresh)
	}
	return e.FinishJob(ctx, c, result, job)
}

// Update shared system monitor samples and refresh their bound displays
func (e *Plugin) applyStatus(ctx context.Context, c *sdk.Client, result jobResult, refresh bool) error {
	value, ok := result.Value.(probeStatus)
	if !ok {
		return nil
	}
	now := time.Now()
	// Update current sample owners while retaining refresh and failure-backoff policy
	return engine.CompleteSample(e.Engine, result.ID, func(id string, b binding, previous sampledStatus) sampledStatus {
		interval := time.Second
		if action(b.Action) == "disk" {
			interval = 5 * time.Second
		}

		configured := engine.RefreshInterval("system-monitor", action(b.Action), b.Settings, interval)
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
	}, func(id string, status displayStatus) error { return e.SendDisplay(ctx, c, id, status) }, func() {
		if !value.NetworkTime.IsZero() {
			e.lastNetwork = value.Network
			e.lastNetworkTime = value.NetworkTime
		}
	})
}

// Process system monitor metadata and lifecycle updates before common dispatch
func (e *Plugin) beforeEvent(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	if ev.Event == "didReceiveTelemetry" {
		var snapshot sdk.TelemetrySnapshot
		if err := json.Unmarshal(ev.Payload, &snapshot); err != nil {
			return true, err
		}
		e.sharedTelemetry = &snapshot
		for id := range e.Bindings {
			e.InvalidateProbe(id)
		}
		return true, e.renderSharedTelemetry(ctx, c, time.Now())
	}
	return false, nil
}

// Clear system monitor transient resources during feature shutdown
func (e *Plugin) closeFeature() { e.sharedTelemetry = nil }

// Reuse a current system monitor sample or schedule a bounded refresh
func (e *Plugin) collectStatus(ctx context.Context, id string, b binding) displayStatus {
	if e.sharedTelemetry != nil {
		return e.sharedSystemStatus(b)
	}

	previous := e.Statuses[id]
	key := e.ProbeKey(id, b)
	// Snapshot binding settings before handing work to a background probe
	return engine.CollectSample(e.Engine, id, key, previous, time.Now, func() bool {

		request := statusRequest{binding: b, network: e.lastNetwork, networkTime: e.lastNetworkTime}
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

// Build the current system monitor status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("system-monitor", action(b.Action), text, err)
}

// Route system monitor inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating system monitor input to its action implementation
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

	return e.Render(ctx, c, ev.Context)
}

// Expose the system monitor feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Before: e.beforeEvent, After: e.afterEvent, Action: e.handleAction, Inspector: e.extensionInspector, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, Collect: e.collectStatus, Poll: e.pollFeature}
}

// Choose the next explicit system monitor deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	if len(e.Bindings) == 0 {
		e.telemetryExpiry = time.Time{}
		return e.PendingWrite
	}
	if e.sharedTelemetry != nil {
		return e.nextTelemetryPoll(now)
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

			if slowStatus("system-monitor") && !(e.sharedTelemetry != nil) {
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

// Choose the next deadline for shared host telemetry rendering
func (e *Plugin) nextTelemetryPoll(now time.Time) time.Time {
	deadline := e.telemetryExpiry

	if deadline.IsZero() || now.Before(deadline) {
		deadline = time.Time{}
		for _, b := range e.Bindings {
			value := e.sharedSystemMeasurement(b)
			if !value.Fresh(now) {
				continue
			}

			expires := value.ObservedAt + value.MaxAgeMS + 1
			if expires <= value.ObservedAt || expires > math.MaxInt64 {
				continue
			}
			at := time.UnixMilli(int64(expires))
			if deadline.IsZero() || at.Before(deadline) {
				deadline = at
			}
		}
		e.telemetryExpiry = deadline
	}
	if !e.PendingWrite.IsZero() && (deadline.IsZero() || e.PendingWrite.Before(deadline)) {
		return e.PendingWrite
	}
	return deadline
}

// Run due system monitor feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	if err := e.subscribeTelemetry(ctx, c); err != nil {
		return true, err
	}
	if e.sharedTelemetry != nil {
		return true, e.renderSharedTelemetry(ctx, c, time.Now())
	}
	return false, nil
}

// Refresh bound system views from the shared telemetry snapshot
func (e *Plugin) renderSharedTelemetry(ctx context.Context, c *sdk.Client, now time.Time) error {
	for id, b := range e.Bindings {
		if err := e.SendDisplay(ctx, c, id, e.sharedSystemStatusAt(b, now)); err != nil {
			return err
		}
	}
	e.telemetryExpiry = time.Time{}
	return nil
}

// Read a detached system monitor status request away from binding mutation
func (r statusRequest) sample(ctx context.Context) probeStatus {
	sample := Plugin{lastNetwork: r.network, lastNetworkTime: r.networkTime}
	status := sample.displayStatus(ctx, "", r.binding)
	return probeStatus{Status: status, Network: sample.lastNetwork, NetworkTime: sample.lastNetworkTime}
}

// Select the host telemetry metric requested by a binding
func (e *Plugin) sharedSystemMeasurement(b binding) sdk.Measurement {
	switch action(b.Action) {
	case "cpu":
		return e.sharedTelemetry.CPU
	case "memory":
		return e.sharedTelemetry.Memory
	case "network":
		return e.sharedTelemetry.Network
	case "disk":
		var value sdk.Measurement
		selected := resolvedDiskPath(configvalue.String(b.Settings, "path", ""))
		longest := 0
		for mount, measurement := range e.sharedTelemetry.Disks {
			if diskPathContains(mount, selected) && len(mount) >= longest {
				value = measurement
				longest = len(mount)
			}
		}
		return value
	}
	return sdk.Measurement{}
}

// Build a display status from the current host measurement
func (e *Plugin) sharedSystemStatus(b binding) displayStatus {
	return e.sharedSystemStatusAt(b, time.Now())
}

// Build telemetry display status with explicit freshness time
func (e *Plugin) sharedSystemStatusAt(b binding, now time.Time) displayStatus {
	value := e.sharedSystemMeasurement(b)
	label := ""
	format := "%.1f%%"
	switch action(b.Action) {
	case "cpu":
		label = "CPU"
	case "memory":
		label = "RAM"
	case "network":
		label = "Network"
		format = "%.1f KiB/s"
	case "disk":
		label = "Disk"
	}
	if value.Value == nil {
		return displayStatus{Text: label + "\nUnavailable", Unavailable: true}
	}
	if !value.Fresh(now) {
		return displayStatus{Text: label + "\nStale", Unavailable: true}
	}
	number := *value.Value
	if action(b.Action) == "network" {
		number /= 1024
	}
	return displayStatus{Text: label + " " + fmt.Sprintf(format, number)}
}

// Schedule bounded system monitor work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Subscribe to the host metrics required by active system bindings
func (e *Plugin) subscribeTelemetry(ctx context.Context, c *sdk.Client) error {

	topics := map[string]bool{}
	for _, b := range e.Bindings {
		{

			topics[action(b.Action)] = true
		}

	}
	names := make([]string, 0, len(topics))
	for topic := range topics {
		names = append(names, topic)
	}
	sort.Strings(names)
	signature := strings.Join(names, ",")
	if signature == e.telemetrySubscription {
		return nil
	}
	if err := c.SubscribeTelemetry(ctx, names); err != nil {
		return err
	}
	e.telemetrySubscription = signature
	return nil
}

// Compute the system monitor text shown for the selected action
func (e *Plugin) text(ctx context.Context, _ string, b binding) (string, error) {
	s := b.Settings
	a := action(b.Action)
	{

		switch a {
		case "cpu":
			values, err := cpu.PercentWithContext(ctx, 0, false)
			if err != nil {
				return "", err
			}
			if len(values) > 0 {
				return fmt.Sprintf("CPU %.1f%%", values[0]), nil
			}
		case "memory":
			m, err := mem.VirtualMemoryWithContext(ctx)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("RAM %.1f%%", m.UsedPercent), nil
		case "disk":
			d, err := disk.UsageWithContext(ctx, resolvedDiskPath(configvalue.String(s, "path", "")))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Disk %.1f%%", d.UsedPercent), nil
		case "network":
			stats, err := netstat.IOCountersWithContext(ctx, false)
			if err != nil {
				return "", err
			}
			if len(stats) > 0 {
				total := stats[0].BytesRecv + stats[0].BytesSent
				rate := float64(0)
				now := time.Now()
				if !e.lastNetworkTime.IsZero() && total >= e.lastNetwork {
					rate = float64(total-e.lastNetwork) / now.Sub(e.lastNetworkTime).Seconds()
				}
				e.lastNetwork = total
				e.lastNetworkTime = now
				return fmt.Sprintf("Network %.1f KiB/s", rate/1024), nil
			}
		}
	}

	return "system-monitor", nil
}

// Identify status types that require slower background sampling
func slowStatus(slug string) bool {
	switch slug {
	case "system-monitor", "microphone-controls", "battery-power", "network-vpn", "weather":
		return true
	}
	return false
}
