package timerspomodoro

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type Plugin struct {
	*engine.Engine
	deadlines  map[string]*saved
	State      map[string]*saved
	timerViews map[string]timerViewCache
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type pluginDisplay = engine.Display

type saved struct {
	FocusMillis int64   `json:"focus_ms"`
	BreakMillis int64   `json:"break_ms"`
	Value       float64 `json:"value"`
	Deadline    int64   `json:"deadline"`
	Started     int64   `json:"started"`
	Elapsed     int64   `json:"elapsed"`
	Mode        string  `json:"mode"`
	Break       bool    `json:"break"`
	Color       string  `json:"color"`
}

type timerViewCache struct {
	key  timerViewKey
	view pluginDisplay
}

type timerViewKey struct {
	Action, Theme, Icon  string
	Width, Height, State int
	Unavailable          bool
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var emptySaved saved

// Attach timers pomodoro lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize timers pomodoro state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}, timerViews: map[string]timerViewCache{}, deadlines: map[string]*saved{}}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	for k, v := range e.State {
		if v != nil && v.Deadline > 0 {
			e.deadlines[k] = v
		}
	}
	return e
}

// Apply completed timers pomodoro work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	return e.FinishJob(ctx, c, result, job)
}

// Release transient timers pomodoro state belonging to one binding
func (e *Plugin) clearFeature(id string) { delete(e.timerViews, id) }

// Clear timers pomodoro transient resources during feature shutdown
func (e *Plugin) closeFeature() { clear(e.timerViews) }

// Build the current timers pomodoro status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	s := b.Settings

	text, content := timerContent(e.lookupState(id, s), time.Now().UnixMilli())
	if content.Primary == "Done" {
		a := action(b.Action)
		if a != "" {
			content.Label = strings.ToUpper(a[:1]) + a[1:]
		}
	}
	v := e.lookupState(id, s)
	state := 0
	{

		if v.Deadline > 0 || v.Started > 0 {
			state = 1
			if v.Mode == "pomodoro" && v.Break {
				state = 3
			}
		} else if v.Color == "Done" {
			state = 2
		}
	}

	return displayStatus{Text: text, State: state, Content: &content}
}

// Route timers pomodoro inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating timers pomodoro input to its action implementation
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

			if v.Deadline != 0 {
				v.Deadline += int64(ticks) * int64(step) * 1000
			}
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

		now := time.Now().UnixMilli()
		switch a {
		case "reset":
			*v = saved{}
		case "adjust":
			if v.Deadline != 0 {
				v.Deadline += int64(configvalue.Number(s, "step", 60) * 1000)
			}
		case "stopwatch":
			if v.Started == 0 {
				v.Started = now
				v.Mode = a
				v.Color = ""
			} else {
				v.Elapsed += now - v.Started
				v.Started = 0
			}
		default:
			if v.Deadline == 0 {
				duration := configvalue.Number(s, "seconds", 300)
				if a == "pomodoro" {
					duration = configvalue.Number(s, "focus_minutes", 25) * 60
				}
				if duration <= 0 || duration > 604800 {
					return fmt.Errorf("duration must be 1 second to 7 days")
				}
				v.FocusMillis = int64(duration * 1000)
				v.BreakMillis = int64(configvalue.Number(s, "break_minutes", 5) * 60000)
				if v.BreakMillis <= 0 || v.BreakMillis > 604800000 {
					return fmt.Errorf("break duration must be positive and at most 7 days")
				}
				v.Deadline = now + int64(duration*1000)
				v.Mode = a
				v.Color, v.Break = "", false
			} else {
				v.Deadline = 0
			}
		}
	}

	if writesState {
		if err := e.routinePersist(); err != nil {
			return err
		}
	}
	return e.Render(ctx, c, ev.Context)
}

// Expose the timers pomodoro feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Clear: e.clearFeature, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature, View: e.timerDisplayView, DynamicDisplay: true}
}

// Find the saved timers pomodoro state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
}

// Choose the next explicit timers pomodoro deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

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

			state := e.lookupState(id, b.Settings)
			if state.Started > 0 || state.Deadline > 0 {
				earlier(e.NextPeriodic)
			}
		}

	}
	{

		for _, state := range e.deadlines {
			if state != nil && state.Deadline > 0 {
				earlier(time.UnixMilli(state.Deadline))
			}
		}
	}

	earlier(e.PendingWrite)
	return deadline
}

// Run due timers pomodoro feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	now := time.Now().UnixMilli()
	changed := false
	for key, v := range e.deadlines {
		if v != nil && v.Deadline > 0 && v.Deadline <= now {
			if v.Mode == "pomodoro" {
				if v.FocusMillis <= 0 {
					v.FocusMillis = 25 * 60000
				}
				if v.BreakMillis <= 0 {
					v.BreakMillis = 5 * 60000
				}

				cycle := v.FocusMillis + v.BreakMillis
				if elapsed := now - v.Deadline; elapsed > cycle {
					v.Deadline += (elapsed / cycle) * cycle
				}
				for v.Deadline <= now {
					v.Break = !v.Break
					if v.Break {
						v.Deadline += v.BreakMillis
					} else {
						v.Deadline += v.FocusMillis
					}
				}
			} else {
				delete(e.deadlines, key)
				v.Deadline = 0
				v.Color = "Done"
			}
			changed = true
		}
	}
	if changed {
		if err := engine.Persist(e.Engine); err != nil {
			return true, err
		}
	}
	return false, nil
}

// Flush changed timers pomodoro state through the engine persistence policy
func (e *Plugin) routinePersist() error {
	engine.MarkDirty(e.Engine)
	{

		for key, value := range e.deadlines {
			if value.Deadline <= 0 {
				delete(e.deadlines, key)
			}
		}
	}

	return engine.Persist(e.Engine)

}

// Resolve or initialize the timers pomodoro state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)
	{

		e.deadlines[key] = v
	}

	return v
}

// Produce both compatibility text and semantic rows from timer state. Neither
// rendering nor localization needs to reverse-engineer a formatted title
func timerContent(v *saved, now int64) (string, engine.Presentation) {
	ms := v.Elapsed
	if v.Started > 0 {
		ms += now - v.Started
	}
	if v.Deadline > 0 {
		ms = v.Deadline - now
	}
	ms = max(0, ms)
	label := v.Mode
	if label == "" {
		label = "Ready"
	}
	if v.Break {
		label = "Break"
	}
	if v.Color == "Done" {
		return "Done", engine.Presentation{Kind: "progress", Primary: "Done"}
	}
	value := fmt.Sprintf("%02d:%02d", ms/60000, ms/1000%60)
	return label + "\n" + value, engine.Presentation{Kind: "progress", Primary: value, Label: label}
}

// Build the timer display from its current phase and remaining time
func (e *Plugin) timerDisplayView(id string, status displayStatus) pluginDisplay {
	b := e.Bindings[id]
	width, height := 112, 112
	if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
		width, height = b.Surface.Width, b.Surface.Height
	}
	key := timerViewKey{b.Action, e.Theme, status.Icon, width, height, status.State, status.Unavailable}
	cached, known := e.timerViews[id]
	owned := !known || key != cached.key
	if owned {
		cached = timerViewCache{key: key, view: engine.CoordinatedView(e.Engine, id, status)}
	} else if cached.view.Title != status.Text {
		cached.view = engine.CoordinatedView(e.Engine, id, status)
		owned = true
	}
	view := cached.view
	v := e.lookupState(id, b.Settings)
	duration := v.FocusMillis
	if v.Break {
		duration = v.BreakMillis
	}
	if duration > 0 && v.Deadline > 0 {
		progress := min(100.0, max(0.0, 100*(1-float64(v.Deadline-time.Now().UnixMilli())/float64(duration))))
		indicator, _ := view.Feedback["indicator"].(map[string]any)
		if indicator["value"] != progress {
			if !owned {
				view.Feedback = cloneFeedback(view.Feedback)
			}
			view.Feedback["indicator"] = map[string]any{"value": progress, "rect": [4]int{4, 97, 192, 3}}
		}
	} else if indicator, _ := view.Feedback["indicator"].(map[string]any); indicator["value"] != nil {
		if !owned {
			view.Feedback = cloneFeedback(view.Feedback)
		}
		view.Feedback["indicator"] = map[string]any{"rect": [4]int{0, 0, 0, 0}}
	}
	cached.view = view
	e.timerViews[id] = cached
	return view
}
