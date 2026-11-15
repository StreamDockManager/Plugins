package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/managedstate"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// localStateClient constructs the isolated local-state client
func localStateClient(t *testing.T, raw string, answer func(sdk.Event) sdk.Event) *sdk.Client {
	t.Helper()
	info, err := sdk.ParseHostInfo([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	client, events := sdk.NewLocalClient(info, "opaque-registration")
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case event := <-events:
				response := answer(event)
				if response.Event != "" {
					client.ObserveControl(response)
				}
			case <-done:
				return
			}
		}
	}()
	return client
}

// TestManagedPortableStateUsesBaseHostAndPreservesUnknownSettings verifies managed portable state uses base host and preserves unknown settings
func TestManagedPortableStateUsesBaseHostAndPreservesUnknownSettings(t *testing.T) {
	legacy := t.TempDir()
	path := filepath.Join(legacy, "state.json")
	os.WriteFile(path, []byte(`{"should_not_import":true}`), 0600)
	t.Setenv("STREAM_DOCK_MANAGER_PLUGIN_STATE_DIR", legacy)
	state := map[string]map[string]any{}
	e := newManaged(Definition{Slug: "counter-tally"}, func(e *Engine) Feature { Restore(e, &state); return Feature{Snapshot: func() any { return state }} })
	defer e.Close()
	writes := make(chan map[string]any, 1)
	c := localStateClient(t, `{}`, func(event sdk.Event) sdk.Event {
		if event.Event == "getGlobalSettings" {
			return sdk.Message("didReceiveGlobalSettings", "opaque-registration", map[string]any{"settings": map[string]any{"unknown": map[string]any{"keep": true}, portableStateKey: map[string]any{"shared": map[string]any{"value": 4}}}})
		}
		if event.Event == "setGlobalSettings" {
			var value map[string]any
			json.Unmarshal(event.Payload, &value)
			writes <- value
		}
		return sdk.Event{}
	})
	if _, err := e.Feature.Before(context.Background(), c, sdk.Event{}); err != nil {
		t.Fatal(err)
	}
	if state["shared"]["value"] != float64(4) {
		t.Fatal(state)
	}
	state["shared"]["value"] = 5
	MarkDirty(e)
	if err := RoutinePersist(e); err != nil {
		t.Fatal(err)
	}
	value := <-writes
	if !reflect.DeepEqual(value["unknown"], map[string]any{"keep": true}) {
		t.Fatal(value)
	}
	if e.DirtyState || !e.PendingWrite.IsZero() || e.Path != "" {
		t.Fatal("managed state scheduled a local write")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"should_not_import":true}` {
		t.Fatal("legacy file touched")
	}
}

// TestManagedPrivateStatePartitionsAndNeverFallsBackAfterFailure verifies managed private state partitions and never falls back after failure
func TestManagedPrivateStatePartitionsAndNeverFallsBackAfterFailure(t *testing.T) {
	state := map[string]map[string]any{}
	e := newManaged(Definition{Slug: "internet-speed-test"}, func(e *Engine) Feature { Restore(e, &state); return Feature{Snapshot: func() any { return state }} })
	defer e.Close()
	commits := make(chan map[string]any, 2)
	c := localStateClient(t, `{"streamDockManager":{"contract_version":1,"capabilities":["plugin-storage-v1"]}}`, func(event sdk.Event) sdk.Event {
		var payload map[string]any
		json.Unmarshal(event.Payload, &payload)
		response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: event.ProtocolID, RequestID: event.RequestID}
		switch event.Method {
		case "listKeys":
			response.Result = json.RawMessage(`{"keys":[],"generation":"7","revision":"0"}`)
		case "commit":
			commits <- payload
			response.Error = &sdk.RPCError{Code: "storage_failure", Message: "test failure"}
		default:
			t.Error("unexpected fallback", event.Event, event.Method)
		}
		return response
	})
	if _, err := e.Feature.Before(context.Background(), c, sdk.Event{}); err != nil {
		t.Fatal(err)
	}
	state["speed-history"] = map[string]any{"speed_results": []any{map[string]any{"download": 100}}}
	MarkDirty(e)
	if err := Persist(e); err == nil {
		t.Fatal("failure reported success")
	}
	payload := <-commits
	mutations := payload["mutations"].([]any)
	mutation := mutations[0].(map[string]any)
	if len(mutations) != 1 || mutation["key"] != fmt.Sprintf("record:%x", sha256.Sum256([]byte("speed-history"))) || mutation["value"].(map[string]any)["identity"] != "speed-history" {
		t.Fatal(payload)
	}
	managed, _ := managedStates.Load(e)
	if !managed.(*managedState).dirty || managed.(*managedState).state.CheckPending() == nil {
		t.Fatal("failed snapshot was discarded")
	}
	if e.DirtyState || e.Path != "" {
		t.Fatal("failed host write scheduled file fallback")
	}
	if err := (&managedHandler{Engine: e}).Close(); err == nil {
		t.Fatal("shutdown concealed an unconfirmed host write")
	}
}

// TestOptionalPrivateHistoryKeepsLegacyActionsAvailable verifies optional private history keeps legacy actions available
func TestOptionalPrivateHistoryKeepsLegacyActionsAvailable(t *testing.T) {
	state := map[string]any{}
	calls := 0
	e := newManaged(Definition{Slug: "randomizer-dice"}, func(e *Engine) Feature {
		Restore(e, &state)
		return Feature{Snapshot: func() any { return state }, Before: func(context.Context, *sdk.Client, sdk.Event) (bool, error) {
			calls++
			state["draw"] = "card"
			MarkDirty(e)
			return false, RoutinePersist(e)
		}}
	})
	defer e.Close()
	c := localStateClient(t, `{}`, func(event sdk.Event) sdk.Event {
		if event.Event != "logMessage" {
			t.Error("legacy private fallback traffic", event.Event)
		}
		return sdk.Event{}
	})
	if _, err := e.Feature.Before(context.Background(), c, sdk.Event{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || state["draw"] != "card" || e.DirtyState {
		t.Fatal("ordinary action was disabled")
	}
}

// TestRestoredTimersPauseWithoutReplayingElapsedActions verifies restored timers pause without replaying elapsed actions
func TestRestoredTimersPauseWithoutReplayingElapsedActions(t *testing.T) {
	state := map[string]map[string]any{}
	e := newManaged(Definition{Slug: "timers-pomodoro"}, func(e *Engine) Feature { Restore(e, &state); return Feature{Snapshot: func() any { return state }} })
	defer e.Close()
	c := localStateClient(t, `{}`, func(event sdk.Event) sdk.Event {
		return sdk.Message("didReceiveGlobalSettings", "opaque-registration", map[string]any{"settings": map[string]any{portableStateKey: map[string]any{"timer": map[string]any{"deadline": 1, "started": 2, "elapsed": 50}}}})
	})
	if _, err := e.Feature.Before(context.Background(), c, sdk.Event{}); err != nil {
		t.Fatal(err)
	}
	if state["timer"]["deadline"] != float64(0) || state["timer"]["started"] != float64(0) || state["timer"]["elapsed"] != float64(50) || state["timer"]["color"] != "Paused" {
		t.Fatal(state)
	}
}

// TestPrivateRestoreRejectsNonAdjacentCursorCycle verifies private restore rejects non adjacent cursor cycle
func TestPrivateRestoreRejectsNonAdjacentCursorCycle(t *testing.T) {
	calls := 0
	c := localStateClient(t, `{"streamDockManager":{"contract_version":1,"capabilities":["plugin-storage-v1"]}}`, func(event sdk.Event) sdk.Event {
		cursors := []string{"a", "b", "a"}
		if event.Method != "listKeys" || calls >= len(cursors) {
			t.Error("restore exceeded cursor cycle", event.Method, calls)
			return sdk.Event{}
		}
		result, _ := json.Marshal(map[string]any{"keys": []string{}, "generation": "1", "revision": "0", "cursor": cursors[calls]})
		calls++
		return sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: event.ProtocolID, RequestID: event.RequestID, Result: result}
	})
	store, err := sdk.NewHostStore(c, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := managedstate.New("private", "", nil, store)
	if _, err = state.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "repeated storage cursor") {
		t.Fatal("cursor cycle accepted", err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
