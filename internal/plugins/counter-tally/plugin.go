package countertally

import (
	"context"
	"strconv"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type Plugin struct {
	*engine.Engine
	State map[string]*saved
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type saved struct {
	Value    float64 `json:"value"`
	Deadline int64   `json:"deadline"`
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

var emptySaved saved

// Attach counter tally lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize counter tally state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	return e
}

// Apply completed counter tally work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	return e.FinishJob(ctx, c, result, job)
}

// Build the current counter tally status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("counter-tally", action(b.Action), text, err)
}

// Route counter tally inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating counter tally input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	s := b.Settings
	if !active(ev) && ev.Event != "dialRotate" {
		return nil
	}
	a := action(ev.Action)
	writesState := true

	v := e.lookupState(ev.Context, s)
	if writesState {
		v = e.state(ev.Context, s)
	}
	if ev.Event == "dialRotate" {
		delete(e.Effective, ev.Context)
		ticks := ev.Data().Ticks
		step := configvalue.Number(s, "step", 1)
		{

			v.Value += float64(ticks) * step
		}

		if writesState {
			if err := e.routinePersist(); err != nil {
				return err
			}
		}
		return e.Render(ctx, c, ev.Context)
	}
	if !active(ev) {
		return nil
	}
	{

		switch a {
		case "increment":
			v.Value += configvalue.Number(s, "step", 1)
		case "decrement":
			v.Value -= configvalue.Number(s, "step", 1)
		case "reset":
			v.Value = configvalue.Number(s, "reset", 0)
		}
	}

	if writesState {
		if err := e.routinePersist(); err != nil {
			return err
		}
	}
	return e.Render(ctx, c, ev.Context)
}

// Expose the counter tally feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals, AdditiveDial: func(a string) bool { return engine.AdditiveDial(Definition.Slug, a) }}
}

// Find the saved counter tally state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
}

// Choose the next explicit counter tally deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	return e.PendingWrite

}

// Flush changed counter tally state through the engine persistence policy
func (e *Plugin) routinePersist() error { return engine.RoutinePersist(e.Engine) }

// Resolve or initialize the counter tally state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)

	return v
}

// Compute the counter tally text shown for the selected action
func (e *Plugin) text(_ context.Context, id string, b binding) (string, error) {
	s := b.Settings
	v := e.lookupState(id, s)

	return strconv.FormatFloat(v.Value, 'g', -1, 64), nil

}
