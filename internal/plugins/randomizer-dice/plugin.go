package randomizerdice

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
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
	Value    float64  `json:"value"`
	Deadline int64    `json:"deadline"`
	Draws    []string `json:"draws"`
	Color    string   `json:"color"`
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var emptySaved saved

// Attach randomizer dice lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize randomizer dice state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	return e
}

// Apply completed randomizer dice work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	return e.FinishJob(ctx, c, result, job)
}

// Build the current randomizer dice status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("randomizer-dice", action(b.Action), text, err)
}

// Route randomizer dice inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating randomizer dice input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	if !active(ev) {
		return nil
	}
	s := b.Settings
	a := action(ev.Action)
	// Validate and choose a result before creating or changing persistent state
	v := *e.lookupState(ev.Context, s)
	switch a {
	case "reset":
		v.Draws = nil
		v.Color = ""
	case "dice", "range":
		lo, hi := int64(1), int64(6)
		var err error
		if a == "dice" {
			hi, err = integerSetting(s, "sides", 6, 2, 1000000)
		} else {
			lo, err = integerSetting(s, "min", 1, -1e9, 1e9)
			if err == nil {
				hi, err = integerSetting(s, "max", 100, -1e9, 1e9)
			}
		}
		if err != nil {
			return err
		}
		if hi < lo {
			return fmt.Errorf("maximum must be greater than or equal to minimum")
		}
		n, err := rand.Int(rand.Reader, big.NewInt(hi-lo+1))
		if err != nil {
			return err
		}
		v.Value = float64(lo + n.Int64())
	case "list", "draw":
		if raw, exists := s["items"]; exists {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("items must contain one item per line")
			}
		}
		items := configvalue.String(s, "items", "")
		if a == "draw" && strings.TrimSpace(items) == "" {
			items = defaultDeck
		}
		pool := make([]string, 0)
		used := make(map[string]bool, len(v.Draws))
		if a == "draw" {
			for _, item := range v.Draws {
				used[item] = true
			}
		}
		for item := range strings.SplitSeq(items, "\n") {
			item = strings.TrimSpace(item)
			if item != "" && !used[item] {
				pool = append(pool, item)
			}
		}
		if len(pool) == 0 {
			if a == "list" {
				return fmt.Errorf("configure at least one item in Items (one per line)")
			}
			return fmt.Errorf("no remaining items; use Reset with the same Shared state name")
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pool))))
		if err != nil {
			return err
		}
		v.Color = pool[n.Int64()]
		if a == "draw" {
			v.Draws = append(v.Draws, v.Color)
		}
	default:
		return fmt.Errorf("unknown randomizer action %q", a)
	}
	*e.state(ev.Context, s) = v
	if err := e.routinePersist(); err != nil {
		return err
	}
	return e.Render(ctx, c, ev.Context)
}

var defaultDeck = func() string {
	var cards []string
	for _, suit := range []string{"Clubs", "Diamonds", "Hearts", "Spades"} {
		for _, rank := range []string{"Ace", "2", "3", "4", "5", "6", "7", "8", "9", "10", "Jack", "Queen", "King"} {
			cards = append(cards, rank+" of "+suit)
		}
	}
	return strings.Join(cards, "\n")
}()

// Read and validate a bounded integer action setting
func integerSetting(s map[string]any, key string, fallback, minimum, maximum int64) (int64, error) {
	value := float64(fallback)
	var err error
	if raw, exists := s[key]; exists {
		switch raw := raw.(type) {
		case float64:
			value = raw
		default:
			err = fmt.Errorf("invalid number")
		}
	}
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < float64(minimum) || value > float64(maximum) {
		return 0, fmt.Errorf("%s must be a numeric whole number between %d and %d", key, minimum, maximum)
	}
	return int64(value), nil
}

// Expose the randomizer dice feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals}
}

// Find the saved randomizer dice state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
}

// Choose the next explicit randomizer dice deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	return e.PendingWrite

}

// Flush changed randomizer dice state through the engine persistence policy
func (e *Plugin) routinePersist() error { return engine.RoutinePersist(e.Engine) }

// Resolve or initialize the randomizer dice state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)

	return v
}

// Compute the randomizer dice text shown for the selected action
func (e *Plugin) text(_ context.Context, id string, b binding) (string, error) {
	s := b.Settings
	v := e.lookupState(id, s)
	a := action(b.Action)
	{

		if a == "list" || a == "draw" {
			return v.Color, nil
		}
		if a == "reset" {
			return "Ready", nil
		}
		return strconv.FormatFloat(v.Value, 'g', -1, 64), nil
	}

}
