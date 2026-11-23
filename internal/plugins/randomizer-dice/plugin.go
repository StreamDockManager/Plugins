package randomizerdice

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type Plugin struct {
	*engine.Engine
	State              map[string]*saved
	rolls              map[string]dieRoll
	heldTotals         map[string]heldRollTotal
	badgeFades         map[string]badgeFade
	balls              map[string]ballRoll
	coins              map[string]dieRoll
	rollSequence       uint64
	visibleDevices     map[string]string
	themeColors        sdk.ThemeColors
	configuredCounters map[string]bool
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type saved struct {
	RollTotal  uint64   `json:"roll_total,omitempty"`
	RollCount  uint64   `json:"roll_count,omitempty"`
	CoinFace   int      `json:"coin_face,omitempty"`
	BallAnswer string   `json:"ball_answer,omitempty"`
	Value      float64  `json:"value"`
	Deadline   int64    `json:"deadline"`
	Draws      []string `json:"draws"`
	Color      string   `json:"color"`
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var emptySaved saved

// Attach randomizer dice lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize randomizer dice state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}, visibleDevices: map[string]string{}, rolls: make(map[string]dieRoll), heldTotals: make(map[string]heldRollTotal), badgeFades: make(map[string]badgeFade), balls: make(map[string]ballRoll), coins: make(map[string]dieRoll)}
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
	if action(b.Action) == "heads-or-tails" {
		return e.coinStatus(id)
	}
	if action(b.Action) == "roll-all" {
		return displayStatus{Text: "Roll All", Icon: "icons/roll-all", Content: &engine.Presentation{Kind: "command", Label: "Roll All"}}
	}
	if action(b.Action) == "dice-roll-counter" {
		return e.counterStatus()
	}
	if action(b.Action) == totalCounterAction {
		return e.totalCounterStatus()
	}
	if action(b.Action) == "magic-8-ball" {
		return e.ballStatus(id)
	}
	if action(b.Action) == "dice" {
		return e.dieStatus(id, b)
	}
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
	if action(ev.Action) == "roll-all" {
		return e.rollAll(ctx, c, ev)
	}
	s := b.Settings
	a := action(ev.Action)
	if a == countCounterAction || a == totalCounterAction {
		if e.resetCounter(a) {
			if err := e.routinePersist(); err != nil {
				return err
			}
		}
		return e.refreshCounters(c, a)
	}
	count := a == "dice" && e.counterEnabled(countCounterAction)
	total := a == "dice" && e.counterEnabled(totalCounterAction)
	if count && e.lookupState(counterStateKey, nil).RollCount == math.MaxUint64 {
		return fmt.Errorf("dice roll counter is full; reset it before rolling")
	}
	if a == "heads-or-tails" {
		return e.flipCoin(ctx, c, ev.Context)
	}
	if a == "magic-8-ball" {
		return e.shakeBall(ctx, c, ev.Context)
	}
	// Validate and choose a result before creating or changing persistent state
	v := *e.lookupState(ev.Context, s)
	var sides int64
	switch a {
	case "reset":
		v.Draws = nil
		v.Color = ""
	case "dice", "range":
		lo, hi := int64(1), int64(6)
		var err error
		if a == "dice" {
			hi, err = dieSides(s)
			sides = hi
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
		var items string
		if raw, exists := s["items"]; exists {
			var ok bool
			if items, ok = raw.(string); !ok {
				return fmt.Errorf("items must contain one item per line")
			}
		} else if a == "list" {
			items = defaultListItems
		}
		usingDefaultDeck := a == "draw" && strings.TrimSpace(items) == ""
		if usingDefaultDeck {
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
			message := "No Items Left"
			if usingDefaultDeck {
				message = "No Cards Left"
			}
			const hint = "Press Reset"
			return e.SendDisplay(ctx, c, ev.Context, displayStatus{
				Text:    message + "\n" + hint,
				Content: &engine.Presentation{Kind: "value", Primary: message, Label: hint},
			})
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pool))))
		if err != nil {
			return err
		}
		v.Color = strings.Clone(pool[n.Int64()])
		if a == "draw" {
			v.Draws = append(v.Draws, v.Color)
		}
	default:
		return fmt.Errorf("unknown randomizer action %q", a)
	}
	if total && e.lookupState(counterStateKey, nil).RollTotal > math.MaxUint64-uint64(v.Value) {
		return fmt.Errorf("dice total counter is full; reset it before rolling")
	}
	if count {
		e.state(counterStateKey, nil).RollCount++
	}
	if total {
		e.state(counterStateKey, nil).RollTotal += uint64(v.Value)
	}
	*e.state(ev.Context, s) = v
	if err := e.routinePersist(); err != nil {
		return err
	}
	if a == "dice" {
		e.Results[ev.Context] = v.Value
		variant := int(e.rollSequence % 4)
		e.rollSequence++
		roll := dieRoll{start: e.Time(), sides: sides, result: int(v.Value), variant: variant}
		e.rolls[ev.Context] = roll
		if total {
			e.holdTotal(ev.Context, roll, s)
		}
	}
	if err := e.Render(ctx, c, ev.Context); err != nil {
		return err
	}
	if count {
		return e.refreshCounters(c, countCounterAction)
	}
	return nil
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
	return engine.Feature{
		Before: func(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
			if ev.Event == "streamDockManagerPermissionsChanged" {
				return true, e.refreshVaultItemDisplays(ctx, c)
			}
			if ev.Event == sdk.ThemeEvent {
				e.clearVaultItemDisplays()
			}
			if ev.Event == configuredActionsEvent {
				return true, e.updateConfiguredCounters(ev.Payload)
			}
			if ev.Event == "willAppear" {
				e.trackVisibleDevice(ev)
			}
			if ev.Event == "willDisappear" {
				delete(e.visibleDevices, ev.Context)
			}
			if ev.Event == "deviceDidDisconnect" || ev.Event == "deviceDisconnected" {
				for id, device := range e.visibleDevices {
					if device == ev.Device {
						delete(e.visibleDevices, id)
					}
				}
			}
			if c != nil {
				if theme := c.CurrentPluginTheme(); theme != nil {
					e.themeColors = theme.Theme
				}
			}
			if ev.Event == "willAppear" && (action(ev.Action) == "dice" || action(ev.Action) == "magic-8-ball" || action(ev.Action) == "heads-or-tails") {
				delete(e.rolls, ev.Context)
				delete(e.badgeFades, ev.Context)
				delete(e.balls, ev.Context)
				delete(e.coins, ev.Context)
				delete(e.Results, ev.Context)
			}
			return false, nil
		},
		Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, View: e.displayView,
		FilterStatus: e.formatItemStatus,
		After: func(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
			if ev.Event == sdk.ThemeEvent {
				return e.refreshVaultItemDisplays(ctx, c)
			}
			return nil
		},
		Deadline: e.nextRollFrame, Poll: e.pollRolls, Clear: func(id string) {
			delete(e.rolls, id)
			delete(e.badgeFades, id)
			delete(e.balls, id)
			delete(e.coins, id)
		},
		Close: func() { clear(e.rolls); clear(e.heldTotals); clear(e.badgeFades); clear(e.balls); clear(e.coins) }, Snapshot: func() any { return e.State },
		Apply: e.applyResult, Visuals: visuals,
	}
}

// Find the saved randomizer dice state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
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
