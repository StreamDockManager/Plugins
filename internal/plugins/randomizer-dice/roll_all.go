package randomizerdice

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"math"
	"math/big"
	"sort"
)

func (e *Plugin) trackVisibleDevice(ev sdk.Event) {
	delete(e.visibleDevices, ev.Context)
	var payload struct {
		Multi json.RawMessage `json:"isInMultiAction"`
	}
	if len(ev.Payload) > 0 && json.Unmarshal(ev.Payload, &payload) != nil {
		return
	}
	if len(payload.Multi) > 0 && string(payload.Multi) != "false" {
		return
	}
	e.visibleDevices[ev.Context] = ev.Device
}

// Hosts expose the active page through willAppear/willDisappear, not a portable
// page ID. Never infer device identity from settings or broadcast unknown scope.
func (e *Plugin) rollAll(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	device := e.visibleDevices[ev.Context]
	if device == "" || ev.Device != "" && ev.Device != device {
		return fmt.Errorf("Roll all needs a visible binding with a known device")
	}
	ids := []string{}
	for id, b := range e.Bindings {
		if action(b.Action) == "dice" && e.visibleDevices[id] == device {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("No visible dice on this device page")
	}
	sort.Strings(ids)
	aggregate := e.lookupState(counterStateKey, nil)
	count := e.counterEnabled(countCounterAction)
	total := e.counterEnabled(totalCounterAction)
	if count && aggregate.RollCount > math.MaxUint64-uint64(len(ids)) {
		return fmt.Errorf("Dice roll counter is full; reset it before rolling all")
	}
	staged := make([]dieRoll, 0, len(ids))
	sum := uint64(0)
	for i, id := range ids {
		sides, err := dieSides(e.Bindings[id].Settings)
		if err != nil {
			return err
		}
		n, err := rand.Int(rand.Reader, big.NewInt(sides))
		if err != nil {
			return err
		}
		value := uint64(n.Int64() + 1)
		if total && (sum > math.MaxUint64-value || aggregate.RollTotal > math.MaxUint64-sum-value) {
			return fmt.Errorf("Dice total counter is full; reset it before rolling all")
		}
		sum += value
		staged = append(staged, dieRoll{sides: sides, result: int(value), variant: int((e.rollSequence + uint64(i)) % 4)})
	}
	// All validation/randomness finishes before any result or counter changes.
	// The owner loop commits the batch together, then all animations share a clock.
	now := e.Time()
	for i, id := range ids {
		roll := staged[i]
		roll.start = now
		e.state(id, e.Bindings[id].Settings).Value = float64(roll.result)
		e.Results[id] = float64(roll.result)
		e.rolls[id] = roll
		if total {
			e.holdTotal(id, roll, e.Bindings[id].Settings)
		}
	}
	if count {
		e.state(counterStateKey, nil).RollCount += uint64(len(ids))
	}
	if total {
		e.state(counterStateKey, nil).RollTotal += sum
	}
	e.rollSequence += uint64(len(ids))
	if err := e.routinePersist(); err != nil {
		return err
	}
	// Deliberately render only the verified target set, not a broad action-scope
	// permission that could refresh dice on another connected device.
	var first error
	for _, id := range ids {
		if err := e.Render(context.Background(), c, id); err != nil && first == nil {
			first = err
		}
	}
	if count {
		if err := e.refreshCounters(c, countCounterAction); err != nil && first == nil {
			first = err
		}
	}
	return first
}
