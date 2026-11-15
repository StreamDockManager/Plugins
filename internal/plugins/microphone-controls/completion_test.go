package microphonecontrols

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

// Verify verified command does not wait for or fail with follow up observation
func TestVerifiedCommandDoesNotWaitForOrFailWithFollowUpObservation(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	mic := &displayMicrophone{}
	e.mic = &microphone.Controller{Adapter: mic}
	e.readStatus = func(ctx context.Context, _ statusRequest) probeStatus {
		<-ctx.Done()
		return probeStatus{Status: displayStatus{Text: "Unavailable", Unavailable: true}}
	}
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1","plugin-logs-v1","action-results-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	recorder := testkit.New(host)
	for _, request := range []string{"first", "second"} {
		event := sdk.Event{Event: "keyDown", Context: "control", Action: "app.streamdockmanager.plugins.microphone-controls.toggle", Surface: &sdk.Surface{RequestID: request}}
		if err := recorder.Replay(context.Background(), e.Engine, []sdk.Event{event}); err != nil {
			t.Fatal(err)
		}
		receipts := 0
		for _, event := range recorder.Drain() {
			if event.Event == "actionResult" {
				var payload struct {
					RequestID string `json:"request_id"`
					Success   bool   `json:"success"`
				}
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.RequestID != request || !payload.Success {
					t.Fatal("observation changed verified command outcome", payload)
				}
				receipts++
			}
			if event.Event == "logMessage" && strings.Contains(string(event.Payload), "failed or cancelled") {
				t.Fatal("observation contaminated action log", event)
			}
		}
		if receipts != 1 {
			t.Fatal("verified command waited for its status observation", request, receipts)
		}
	}
	if mic.changes != 2 {
		t.Fatal("command was lost or repeated", mic.changes)
	}
	mic.failed = true
	failed := sdk.Event{Event: "keyDown", Context: "control", Action: "app.streamdockmanager.plugins.microphone-controls.toggle", Surface: &sdk.Surface{RequestID: "denied"}}
	if err := recorder.Replay(context.Background(), e.Engine, []sdk.Event{failed}); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for _, event := range recorder.Drain() {
		if event.Event != "actionResult" {
			continue
		}
		var payload struct {
			RequestID string `json:"request_id"`
			Success   bool   `json:"success"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.RequestID != "denied" || payload.Success {
			t.Fatal("native command failure was hidden", payload)
		}
		failures++
	}
	if failures != 1 {
		t.Fatal("native command failure lost its receipt", failures)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for _, event := range recorder.Drain() {
		if event.Event == "actionResult" || event.Event == "logMessage" && strings.Contains(string(event.Payload), "failed or cancelled") {
			t.Fatal("cancelling observation duplicated or failed a completed command", event)
		}
	}
}
