package randomizerdice

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
)

// Build a configured randomizer action input
func randomizerEvent(kind, id, name string, settings map[string]any) sdk.Event {
	payload, _ := json.Marshal(map[string]any{"settings": settings})
	return sdk.Event{Event: kind, Context: id, Action: "app.streamdockmanager.plugins.randomizer-dice." + name, Payload: payload,
		Surface: &sdk.Surface{Width: 176, Height: 112}}
}

// Verify randomizer actions on every surface
func TestRandomizerActionsOnEverySurface(t *testing.T) {
	for _, kind := range []string{"keyDown", "dialDown", "touchTap"} {
		for _, name := range Definition.Actions {
			t.Run(kind+"/"+name, func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
				settings := map[string]any{"sides": float64(2), "min": float64(-7), "max": float64(-7), "items": " \r\n Selected item \r\n\t"}
				if name == "dice" {
					// Hidden settings from another action must not change the die's lower bound
					settings["min"] = float64(99)
				}
				e.State["control"] = &saved{Draws: []string{"Previous item"}, Color: "Previous item"}
				updates := driveDisplays(t, e, []displayStep{
					{event: randomizerEvent("willAppear", "control", name, settings)},
					{event: randomizerEvent(kind, "control", name, nil)},
				})
				v := e.State["control"]
				want := ""
				switch name {
				case "dice":
					if v.Value < 1 || v.Value > 2 {
						t.Fatal("die outside configured sides", v.Value)
					}
					want = "Dice"
				case "range":
					if v.Value != -7 {
						t.Fatal("range did not include its single endpoint", v.Value)
					}
					want = "-7"
				case "list", "draw":
					if v.Color != "Selected item" {
						t.Fatal("did not select the trimmed nonempty item", v.Color)
					}
					count := 1
					if name == "draw" {
						count = 2
					}
					if len(v.Draws) != count {
						t.Fatal("incorrect draw history", v.Draws)
					}
					want = "Selected item"
				case "reset":
					if len(v.Draws) != 0 || v.Color != "" {
						t.Fatal("reset retained draw history or last selection", v)
					}
					want = "Ready"
				}
				requireUpdatedContexts(t, updates[1], "control")
				payload := visualPayload(updates[1], "control", "setFeedback")
				encoded, _ := json.Marshal(payload)
				if !strings.Contains(string(encoded), want) {
					t.Fatalf("result missing from device feedback: %s; want %q", encoded, want)
				}
			})
		}
	}
}

// Assert the error reported for an invalid randomizer action
func expectedActionError(t *testing.T, e *Plugin, event sdk.Event, message string) displayStep {
	t.Helper()
	return displayStep{Render: func(ctx context.Context, c *sdk.Client) error {
		before, _ := json.Marshal(e.State)
		dirty, pending := e.DirtyState, e.PendingWrite
		err := e.Handle(ctx, c, event)
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("got %v; want error containing %q", err, message)
		}
		after, _ := json.Marshal(e.State)
		if string(before) != string(after) || dirty != e.DirtyState || pending != e.PendingWrite {
			t.Error("failed action changed persistent state")
		}
		return nil
	}}
}

// Verify default deck survives restart and reset
func TestDefaultDeckSurvivesRestartAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	settings := map[string]any{"shared_id": "cards", "items": " \n\t"}
	steps := []displayStep{{event: randomizerEvent("willAppear", "draw", "draw", settings)}}
	for range 20 {
		steps = append(steps, displayStep{event: randomizerEvent("keyDown", "draw", "draw", nil)})
	}
	driveDisplays(t, e, steps) // The real SDK shutdown flushes state
	if len(e.State["cards"].Draws) != 20 {
		t.Fatal("default deck did not draw", e.State)
	}
	e = New(path)
	if len(e.State["cards"].Draws) != 20 {
		t.Fatal("draw history was not restored")
	}
	steps = []displayStep{{event: randomizerEvent("willAppear", "draw", "draw", settings)}}
	for range 32 {
		steps = append(steps, displayStep{event: randomizerEvent("keyDown", "draw", "draw", nil)})
	}
	steps = append(steps, expectedActionError(t, e, randomizerEvent("keyDown", "draw", "draw", nil), "no remaining items"),
		displayStep{Render: func(context.Context, *sdk.Client) error {
			cards := slices.Clone(e.State["cards"].Draws)
			slices.Sort(cards)
			want := strings.Split(defaultDeck, "\n")
			slices.Sort(want)
			if len(cards) != 52 || !slices.Equal(cards, want) || len(slices.Compact(cards)) != 52 {
				t.Error("did not draw every card exactly once", cards)
			}
			return nil
		}},
		displayStep{event: randomizerEvent("willAppear", "reset", "reset", map[string]any{"shared_id": "cards"})},
		displayStep{event: randomizerEvent("keyDown", "reset", "reset", nil)},
		displayStep{event: randomizerEvent("keyDown", "draw", "draw", nil)})
	driveDisplays(t, e, steps)
	if len(e.State["cards"].Draws) != 1 {
		t.Fatal("reset did not replenish the deck", e.State["cards"])
	}
}

// Verify custom draw and list replacement and isolation
func TestCustomDrawAndListReplacementAndIsolation(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	steps := []displayStep{
		{event: randomizerEvent("willAppear", "draw", "draw", map[string]any{"items": " Alpha \r\nBeta\nAlpha\n\t", "shared_id": "custom"})},
		{event: randomizerEvent("keyDown", "draw", "draw", nil)},
		{event: randomizerEvent("keyDown", "draw", "draw", nil)},
		expectedActionError(t, e, randomizerEvent("keyDown", "draw", "draw", nil), "no remaining items"),
		{event: randomizerEvent("willAppear", "list", "list", map[string]any{"items": "Alpha", "shared_id": "custom"})},
		{event: randomizerEvent("keyDown", "list", "list", nil)},
		{event: randomizerEvent("keyDown", "list", "list", nil)},
		{event: randomizerEvent("willAppear", "independent", "draw", map[string]any{"items": "Alpha"})},
		{event: randomizerEvent("keyDown", "independent", "draw", nil)},
		{event: randomizerEvent("didReceiveSettings", "draw", "draw", map[string]any{"items": "Gamma", "shared_id": "custom"})},
		{event: randomizerEvent("keyDown", "draw", "draw", nil)},
	}
	updates := driveDisplays(t, e, steps)
	if !slices.Contains(e.State["custom"].Draws, "Alpha") || !slices.Contains(e.State["custom"].Draws, "Beta") || len(e.State["custom"].Draws) != 3 || e.State["custom"].Color != "Gamma" {
		t.Fatal("custom draw history or settings update failed", e.State["custom"])
	}
	if !slices.Equal(e.State["independent"].Draws, []string{"Alpha"}) {
		t.Fatal("independent draw consumed another context's pool")
	}
	requireUpdatedContexts(t, updates[5], "list")
	if len(updates[6]) != 0 {
		t.Fatal("repeating the same list result resent unchanged feedback")
	}
	requireUpdatedContexts(t, updates[10], "draw")
}

// Verify invalid randomizer settings do not mutate state
func TestInvalidRandomizerSettingsDoNotMutateState(t *testing.T) {
	for _, tc := range []struct {
		name, action, key string
		value             any
	}{
		{"die too small", "dice", "sides", float64(1)},
		{"die too large", "dice", "sides", float64(1000001)},
		{"fractional sides", "dice", "sides", 6.5},
		{"invalid number", "dice", "sides", "invalid"},
		{"numeric string", "dice", "sides", "6"},
		{"numeric range string", "range", "min", "-1000000000"},
		{"boolean", "range", "min", true},
		{"nonfinite", "dice", "sides", "NaN"},
		{"infinite", "range", "max", "+Inf"},
		{"overflow", "range", "max", "1e30"},
		{"null", "range", "min", nil},
		{"reversed range", "range", "min", float64(101)},
		{"fractional range", "range", "min", 1.5},
		{"empty list", "list", "items", " \r\n\t"},
		{"invalid list", "list", "items", []string{"Alpha"}},
		{"invalid draw", "draw", "items", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			driveDisplays(t, e, []displayStep{
				{event: randomizerEvent("willAppear", "invalid", tc.action, map[string]any{tc.key: tc.value})},
				expectedActionError(t, e, randomizerEvent("keyDown", "invalid", tc.action, nil), ""),
			})
			if len(e.State) != 0 {
				t.Fatal("invalid settings created saved state")
			}
		})
	}
}

// Verify randomizer numeric defaults and inactive events
func TestRandomizerNumericDefaultsAndInactiveEvents(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	steps := []displayStep{
		{event: randomizerEvent("willAppear", "range", "range", map[string]any{"min": float64(100)})},
		{event: randomizerEvent("keyDown", "range", "range", nil)},
		{event: randomizerEvent("willAppear", "dice", "dice", nil)},
		{event: randomizerEvent("keyDown", "dice", "dice", nil)},
		{event: randomizerEvent("willAppear", "bounds", "range", map[string]any{"min": float64(-1000000000), "max": float64(-1000000000)})},
		{event: randomizerEvent("keyDown", "bounds", "range", nil)},
	}
	driveDisplays(t, e, steps)
	if e.State["range"].Value != 100 || e.State["dice"].Value < 1 || e.State["dice"].Value > 6 || e.State["bounds"].Value != -1e9 {
		t.Fatal("incorrect numeric defaults or bounds", e.State)
	}
	for _, name := range Definition.Actions {
		e := New(filepath.Join(t.TempDir(), "state.json"))
		for _, kind := range []string{"keyUp", "dialUp", "dialRotate"} {
			if err := e.Handle(context.Background(), nil, randomizerEvent(kind, "unused", name, nil)); err != nil {
				t.Fatal(err)
			}
		}
		if len(e.State) != 0 || e.DirtyState || !e.PendingWrite.IsZero() {
			t.Fatal("inactive event changed saved state", name, e.State)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Verify randomizer RPC reports successful actions
func TestRandomizerRPCReportsSuccessfulActions(t *testing.T) {
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-logs-v1","action-results-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Definition.Actions {
		t.Run(name, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			recorder := testkit.New(host)
			settings := map[string]any{"shared_id": "rpc"}
			if name == "list" {
				settings["items"] = "Selected item"
			}
			event := randomizerEvent("keyDown", "control", name, settings)
			event.Surface.RequestID = "request-" + name
			if err := recorder.Replay(context.Background(), e, []sdk.Event{event}); err != nil {
				t.Fatal(err)
			}
			receipts, logs := 0, 0
			for _, message := range recorder.Drain() {
				switch message.Event {
				case "actionResult":
					receipts++
					var result struct {
						RequestID string `json:"request_id"`
						Success   bool   `json:"success"`
					}
					if err := json.Unmarshal(message.Payload, &result); err != nil || !result.Success || result.RequestID != event.Surface.RequestID || message.Context != event.Context {
						t.Fatal("action reported a failed or uncorrelated RPC result", message, result, err)
					}
				case "logMessage":
					logs++
					var log struct{ Message, Stream string }
					if err := json.Unmarshal(message.Payload, &log); err != nil || log.Stream != "stdout" || !strings.Contains(log.Message, event.Action) {
						t.Fatal("action logged a failure", log, err)
					}
				}
			}
			if receipts != 1 || logs != 1 {
				t.Fatal("missing or duplicate completion/log", receipts, logs)
			}
		})
	}
}

// Verify integer setting requires finite numeric integer
func TestIntegerSettingRequiresFiniteNumericInteger(t *testing.T) {
	for _, value := range []any{"6", "-1000000000", "", "invalid", nil, true, 1.5, math.NaN(), math.Inf(1), math.Inf(-1), 1e30, float64(-1000000001), float64(1000000001)} {
		if _, err := integerSetting(map[string]any{"value": value}, "value", 6, -1e9, 1e9); err == nil || !strings.Contains(err.Error(), "numeric whole number") {
			t.Fatalf("accepted invalid numeric setting %v or unclear error: %v", value, err)
		}
	}
	for _, value := range []float64{-1e9, 0, 1e9} {
		got, err := integerSetting(map[string]any{"value": value}, "value", 6, -1e9, 1e9)
		if err != nil || got != int64(value) {
			t.Fatalf("rejected valid setting %v: %v", value, err)
		}
	}
	if got, err := integerSetting(nil, "value", 6, -1e9, 1e9); err != nil || got != 6 {
		t.Fatalf("missing setting lost its default: %d %v", got, err)
	}
}
