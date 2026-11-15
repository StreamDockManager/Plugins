package microphonecontrols

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

type pickerAdapter struct {
	mu        sync.Mutex
	list      func(context.Context) ([]microphone.Device, error)
	devices   map[string]microphone.Device
	defaultID string
	writes    []string
}

// Return the fake microphone device inventory
func (a *pickerAdapter) Devices(ctx context.Context) ([]microphone.Device, error) { return a.list(ctx) }

// Return the fake default microphone identifier
func (a *pickerAdapter) Default(context.Context) (string, error) { return a.defaultID, nil }

// Read the injected microphone state or configured failure
func (a *pickerAdapter) Read(_ context.Context, id string) (microphone.Device, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d, ok := a.devices[id]
	if !ok {
		return d, fmt.Errorf("microphone disconnected")
	}
	return d, nil
}

// Record a mute change in the fake microphone adapter
func (a *pickerAdapter) SetMute(_ context.Context, id string, muted bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.devices[id]
	d.Muted = muted
	a.devices[id] = d
	a.writes = append(a.writes, id)
	return nil
}

// Build a correlated microphone-discovery inspector request
func microphoneRequestEvent(request string) sdk.Event {
	return sdk.Event{Event: "sendToPlugin", Context: "settings-draft:microphone", Payload: json.RawMessage(`{"op":"microphones","scope":"action","request_id":"` + request + `"}`)}
}

// Find and decode the correlated microphone discovery reply
func microphoneReply(t *testing.T, e *Plugin, client *sdk.Client, events <-chan sdk.Event) map[string]any {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case <-e.WakeChannel:
			if err := e.DrainJobs(context.Background(), client); err != nil {
				t.Fatal(err)
			}
		case reply := <-events:
			if reply.Event != "sendToPropertyInspector" || reply.Context != "settings-draft:microphone" {
				t.Fatalf("unexpected reply: %+v", reply)
			}
			var payload map[string]any
			if err := json.Unmarshal(reply.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			return payload
		case <-deadline:
			t.Fatal("microphone discovery did not reply")
		}
	}
}

// Verify microphone discovery lists names without changing mute or saved selection
func TestMicrophoneDiscoveryListsNamesWithoutChangingMuteOrSavedSelection(t *testing.T) {
	for _, mode := range []string{"devices", "empty", "failure"} {
		t.Run(mode, func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			adapter := &pickerAdapter{defaultID: "usb", list: func(context.Context) ([]microphone.Device, error) {
				if mode == "failure" {
					return nil, errors.New("audio service unavailable")
				}
				if mode == "empty" {
					return nil, nil
				}
				return []microphone.Device{{ID: "usb", Name: "USB microphone"}, {ID: "desk", Name: "Desk microphone"}, {ID: "usb", Name: "Duplicate"}, {Name: "Invalid"}}, nil
			}}
			e.mic = &microphone.Controller{Adapter: adapter}
			e.Bindings["control"] = binding{Settings: map[string]any{"device": "saved-id", "unknown": true}}
			client, events := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
			if _, err := e.extensionInspector(context.Background(), client, microphoneRequestEvent("42")); err != nil {
				t.Fatal(err)
			}
			payload := microphoneReply(t, e, client, events)
			if payload["op"] != "microphones" || payload["request_id"] != "42" || payload["available"] != (mode != "failure") {
				t.Fatal(payload)
			}
			if mode == "devices" {
				devices := payload["devices"].([]any)
				if len(devices) != 2 || devices[0].(map[string]any)["name"] != "Desk microphone" || devices[1].(map[string]any)["id"] != "usb" || payload["default_id"] != "usb" {
					t.Fatal(payload)
				}
			}
			if len(adapter.writes) != 0 || e.Bindings["control"].Settings["device"] != "saved-id" {
				t.Fatal("discovery changed microphone state or saved selection")
			}
		})
	}
}

// Verify microphone discovery cancellation and stale replies
func TestMicrophoneDiscoveryCancellationAndStaleReplies(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	adapter := &pickerAdapter{list: func(context.Context) ([]microphone.Device, error) {
		return []microphone.Device{{ID: "desk", Name: "Desk"}}, nil
	}}
	e.mic = &microphone.Controller{Adapter: adapter}
	client, events := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
	e.extensionInspector(context.Background(), client, microphoneRequestEvent("old"))
	select {
	case <-e.WakeChannel:
	case <-time.After(time.Second):
		t.Fatal("first discovery did not finish")
	}
	// The first result is queued but must not reach the newly refreshed picker
	e.extensionInspector(context.Background(), client, microphoneRequestEvent("new"))
	if reply := microphoneReply(t, e, client, events); reply["request_id"] != "new" {
		t.Fatal(reply)
	}

	started, stopped := make(chan struct{}), make(chan struct{})
	adapter.list = func(ctx context.Context) ([]microphone.Device, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}
	e.extensionInspector(context.Background(), client, microphoneRequestEvent("cancelled"))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	e.extensionInspector(context.Background(), client, sdk.Event{Context: "settings-draft:microphone", Payload: json.RawMessage(`{"op":"cancel_microphones","request_id":"cancelled"}`)})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("discovery did not cancel")
	}
	if err := e.DrainJobs(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("cancelled discovery delivered a reply")
	}
}

// Verify all microphone actions use the chosen device
func TestAllMicrophoneActionsUseTheChosenDevice(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	adapter := &pickerAdapter{defaultID: "desk", devices: map[string]microphone.Device{"desk": {ID: "desk"}, "usb": {ID: "usb"}}}
	e.mic = &microphone.Controller{Adapter: adapter}
	e.readStatus = func(context.Context, statusRequest) probeStatus {
		return probeStatus{Status: displayStatus{Text: "Fixture"}}
	}
	client, _ := sdk.NewLocalClient(sdk.HostInfo{}, "fixture")
	for _, name := range []string{"mute", "unmute", "toggle"} {
		b := binding{Action: "app.streamdockmanager.plugins.microphone-controls." + name, Settings: map[string]any{"device": "usb"}}
		if err := e.handleAction(context.Background(), client, sdk.Event{Event: "keyDown", Context: "control", Action: b.Action}, b); err != nil {
			t.Fatal(err)
		}
	}
	if len(adapter.writes) != 3 {
		t.Fatal(adapter.writes)
	}
	for _, id := range adapter.writes {
		if id != "usb" {
			t.Fatal("changed the default instead of the chosen microphone", adapter.writes)
		}
	}
	delete(adapter.devices, "usb")
	if err := e.handleAction(context.Background(), client, sdk.Event{Event: "keyDown", Context: "control", Action: "app.streamdockmanager.plugins.microphone-controls.mute"}, binding{Settings: map[string]any{"device": "usb"}}); err == nil {
		t.Fatal("missing microphone silently fell back to another input")
	}
}
