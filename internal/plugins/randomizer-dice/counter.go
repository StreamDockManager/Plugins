package randomizerdice

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"slices"
	"strconv"
	"time"
)

// Internal aggregate record uses its own field, never Dice/Range Value or Draws.
const (
	configuredActionsEvent = "streamDockManagerConfiguredActions"
	counterStateKey        = "\x00dice-roll-counter"
	countCounterAction     = "dice-roll-counter"
	totalCounterAction     = "dice-total-counter"
)

// Use saved assignments on Manager and visible bindings on hosts without an inventory
func (e *Plugin) counterEnabled(name string) bool {
	if e.configuredCounters != nil {
		return e.configuredCounters[name]
	}
	for id, b := range e.Bindings {
		if _, visible := e.visibleDevices[id]; visible && action(b.Action) == name {
			return true
		}
	}
	return false
}

// Reset only the selected aggregate and discard its unrevealed values
func (e *Plugin) resetCounter(name string) bool {
	aggregate := e.lookupState(counterStateKey, nil)
	switch name {
	case countCounterAction:
		if aggregate.RollCount != 0 {
			e.state(counterStateKey, nil).RollCount = 0
			return true
		}
	case totalCounterAction:
		clear(e.heldTotals)
		if aggregate.RollTotal != 0 {
			e.state(counterStateKey, nil).RollTotal = 0
			return true
		}
	}
	return false
}

// Reconcile persisted aggregates only from the host's committed action inventory
func (e *Plugin) updateConfiguredCounters(payload json.RawMessage) error {
	var inventory struct {
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal(payload, &inventory); err != nil || inventory.Actions == nil {
		return nil
	}
	e.configuredCounters = make(map[string]bool, 2)
	for _, id := range inventory.Actions {
		name := action(id)
		if name == countCounterAction || name == totalCounterAction {
			e.configuredCounters[name] = true
		}
	}
	changed := false
	for _, name := range []string{countCounterAction, totalCounterAction} {
		if !e.counterEnabled(name) && e.resetCounter(name) {
			changed = true
		}
	}
	if changed {
		return e.routinePersist()
	}
	return nil
}

// Hold the value until the last animation for this die finishes
func (e *Plugin) holdTotal(id string, roll dieRoll, settings map[string]any) {
	held := e.heldTotals[id]
	held.amount += uint64(roll.result)
	held.revealAt = roll.start.Add(time.Duration(roll.finalFrame(settings)) * rollFrameInterval)
	e.heldTotals[id] = held
}

func (e *Plugin) counterStatus() displayStatus {
	value := strconv.FormatUint(e.lookupState(counterStateKey, nil).RollCount, 10)
	return displayStatus{Text: value, Icon: "icons/dice-roll-counter", Content: &engine.Presentation{Kind: "value", Primary: value, Label: "Dice rolls"}}
}

// Hold accepted values out of the visible total while their animation can spoil the result
type heldRollTotal struct {
	amount   uint64
	revealAt time.Time
}

// Display only the portion of the durable total that has already been revealed
func (e *Plugin) totalCounterStatus() displayStatus {
	total := e.lookupState(counterStateKey, nil).RollTotal
	for _, held := range e.heldTotals {
		total -= held.amount
	}
	value := strconv.FormatUint(total, 10)
	return displayStatus{Text: value, Icon: "icons/dice-total-counter", Content: &engine.Presentation{Kind: "value", Primary: value, Label: "Dice total"}}
}

func (e *Plugin) refreshCounters(c *sdk.Client, actions ...string) error {
	// This action intentionally aggregates across all contexts. Explicitly render
	// only its bindings, outside the default same-action isolation policy.
	for id, b := range e.Bindings {
		if slices.Contains(actions, action(b.Action)) {
			if err := e.Render(context.Background(), c, id); err != nil {
				return err
			}
		}
	}
	return nil
}
