package randomizerdice

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestRollAllVisibleDeviceScopeAndReveal(t *testing.T) {
	for _, press := range []string{"keyDown", "dialDown", "touchTap"} {
		t.Run(press, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			host := testkit.New(sdk.HostInfo{})
			clock := testkit.NewClock(time.Now())
			e.Now = clock.Now
			send := func(kind, id, action, device string, settings map[string]any, multi bool) error {
				ev := randomizerEvent(kind, id, action, settings)
				ev.Device = device
				if multi {
					ev.Payload, _ = json.Marshal(map[string]any{"settings": settings, "isInMultiAction": true})
				}
				return e.Handle(context.Background(), host.Client, ev)
			}
			for _, v := range []struct {
				id, action, device string
				multi              bool
			}{{"all", "roll-all", "A", false}, {"a", "dice", "A", false}, {"b", "dice", "A", false}, {"other", "dice", "B", false}, {"old-page", "dice", "A", false}, {"nested", "dice", "A", true}, {"unknown", "dice", "", false}, {"range", "range", "A", false}, {"count", countCounterAction, "A", false}, {"total", totalCounterAction, "A", false}} {
				if err := send("willAppear", v.id, v.action, v.device, map[string]any{"sides": float64(20)}, v.multi); err != nil {
					t.Fatal(err)
				}
			}
			if err := send("willDisappear", "old-page", "dice", "A", nil, false); err != nil {
				t.Fatal(err)
			}
			host.Drain()
			if err := send(press, "all", "roll-all", "A", nil, false); err != nil {
				t.Fatal(err)
			}
			messages := host.Drain()
			for _, id := range []string{"a", "b"} {
				if visualPayload(messages, id, "setImage") == nil {
					t.Fatal("first animation not emitted", id)
				}
			}
			for _, id := range []string{"other", "old-page", "nested", "unknown", "range"} {
				if _, ok := e.rolls[id]; ok {
					t.Fatal("wrong scope", id)
				}
				if visualPayload(messages, id, "setImage") != nil {
					t.Fatal("unrelated image", id)
				}
			}
			if len(e.rolls) != 2 || !e.rolls["a"].start.Equal(e.rolls["b"].start) {
				t.Fatal("batch timing")
			}
			a := e.lookupState(counterStateKey, nil)
			if a.RollCount != 2 || a.RollTotal != uint64(e.Results["a"].(float64)+e.Results["b"].(float64)) {
				t.Fatal("wrong aggregates")
			}
			if e.totalCounterStatus().Text != "0" {
				t.Fatal("premature total")
			}
			if err := send("keyUp", "all", "roll-all", "A", nil, false); err != nil {
				t.Fatal(err)
			}
			if a.RollCount != 2 {
				t.Fatal("release double counted")
			}
			clock.Advance(time.Duration(badgeFinalFrame) * rollFrameInterval)
			if err := e.Poll(context.Background(), host.Client); err != nil {
				t.Fatal(err)
			}
			if e.totalCounterStatus().Text == "0" {
				t.Fatal("total not revealed")
			}
			if err := send(press, "all", "roll-all", "A", nil, false); err != nil {
				t.Fatal(err)
			}
			if a.RollCount != 4 {
				t.Fatal("reroll batch counted incorrectly")
			}
			if err := e.Handle(context.Background(), host.Client, sdk.Event{
				Event: configuredActionsEvent, Payload: json.RawMessage(`{"actions":[]}`),
			}); err != nil {
				t.Fatal(err)
			}
			if err := send(press, "all", "roll-all", "A", nil, false); err != nil {
				t.Fatal(err)
			}
			if a.RollCount != 0 || a.RollTotal != 0 || len(e.heldTotals) != 0 || len(e.rolls) != 2 {
				t.Fatal("Roll All tracked disabled counters or failed to animate")
			}
		})
	}
}

func TestRollAllFailsClosedBeforeMutation(t *testing.T) {
	for _, failure := range []string{"unknown", "invalid-die", "overflow", "nested-trigger"} {
		t.Run(failure, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			host := testkit.New(sdk.HostInfo{})
			for _, id := range []string{"a", "b", "all"} {
				action := "dice"
				if id == "all" {
					action = "roll-all"
				}
				settings := map[string]any{"sides": float64(6)}
				if failure == "invalid-die" && id == "b" {
					settings["sides"] = float64(1)
				}
				ev := randomizerEvent("willAppear", id, action, settings)
				ev.Device = "A"
				if failure == "unknown" && id == "all" {
					ev.Device = ""
				}
				if failure == "nested-trigger" && id == "all" {
					ev.Payload = []byte(`{"isInMultiAction":true}`)
				}
				_ = e.Handle(context.Background(), host.Client, ev)
			}
			if failure == "overflow" {
				if err := e.Handle(context.Background(), host.Client, randomizerEvent("willAppear", "total", totalCounterAction, nil)); err != nil {
					t.Fatal(err)
				}
				e.state(counterStateKey, nil).RollTotal = math.MaxUint64
			}
			ev := randomizerEvent("keyDown", "all", "roll-all", nil)
			ev.Device = "A"
			if err := e.Handle(context.Background(), host.Client, ev); err == nil {
				t.Fatal("accepted invalid batch")
			}
			if len(e.rolls) != 0 || e.lookupState(counterStateKey, nil).RollCount != 0 {
				t.Fatal("partial mutation")
			}
		})
	}
}

func TestRollAllVisibilityPayload(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	for _, payload := range []string{"", "null", "{}", `{"isInMultiAction":false}`} {
		e.trackVisibleDevice(sdk.Event{Context: "a", Device: "A", Payload: json.RawMessage(payload)})
		if e.visibleDevices["a"] != "A" {
			t.Fatal("valid lifecycle rejected", payload)
		}
	}
	for _, payload := range []string{`{`, `{"isInMultiAction":true}`, `{"isInMultiAction":"false"}`, `{"isInMultiAction":null}`} {
		e.trackVisibleDevice(sdk.Event{Context: "a", Device: "A", Payload: json.RawMessage(payload)})
		if e.visibleDevices["a"] != "" {
			t.Fatal("ambiguous/nested lifecycle accepted", payload)
		}
	}
}

func TestRollAllDisconnectRetiresOnlyMatchingDevice(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.visibleDevices["a"] = "A"
	e.visibleDevices["b"] = "B"
	_, err := e.hooks().Before(context.Background(), nil, sdk.Event{Event: "deviceDidDisconnect", Device: "A"})
	if err != nil || e.visibleDevices["a"] != "" || e.visibleDevices["b"] != "B" {
		t.Fatal("disconnect crossed device scope")
	}
}
