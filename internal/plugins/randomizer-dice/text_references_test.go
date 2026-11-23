package randomizerdice

import (
	"encoding/json"
	"path/filepath"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// Formatting an authorized result changes only the transient display and rechecks later renders
func TestVaultItemFormatsDisplayWithoutChangingSavedHistory(t *testing.T) {
	const raw = "Item ${vault.11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222}"
	e := New(filepath.Join(t.TempDir(), "state.json"))
	actionID := engine.ID(Definition.Slug) + ".draw"
	e.Bindings["key"] = binding{Action: actionID, Settings: map[string]any{"items": "Other\n" + raw}}
	e.State["key"] = &saved{Color: raw, Draws: []string{raw}}
	info, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-text-references-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	c, requests := sdk.NewLocalClient(info, "fixture")
	for _, allowed := range []bool{true, false} {
		done := make(chan displayStatus, 1)
		go func() {
			status := e.displayStatus(t.Context(), "key", e.Bindings["key"])
			done <- e.formatItemStatus(t.Context(), c, "key", status)
		}()
		select {
		case req := <-requests:
			var fields map[string]string
			if err := json.Unmarshal(req.Payload, &fields); err != nil || req.Method != "format-text" ||
				len(fields) != 2 || fields["action"] != actionID || fields["text"] != raw {
				t.Fatalf("unexpected format request: %s %v", req.Method, err)
			}
			response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID}
			if allowed {
				response.Result = json.RawMessage(`{"text":"Item Hello"}`)
			} else {
				response.Error = &sdk.RPCError{Code: "permission_denied", Message: "private detail"}
			}
			c.ObserveControl(response)
		case <-done:
			t.Fatal("did not recheck the formatted item")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		got := <-done
		if allowed && got.Text != "Item Hello" || !allowed && (!got.Unavailable || got.Text != "Vault text unavailable") {
			t.Fatalf("unexpected rendered result: %+v", got)
		}
	}
	row := e.State["key"]
	if row.Color != raw || len(row.Draws) != 1 || row.Draws[0] != raw || e.Bindings["key"].Settings["items"] != "Other\n"+raw {
		t.Fatal("formatting changed the saved configuration or draw history")
	}
}

// An incomplete token never sends an RPC or displays the token itself
func TestVaultItemIncompleteTemplateFailsClosed(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	e.Bindings["key"] = binding{Action: engine.ID(Definition.Slug) + ".list"}
	literal := e.formatItemStatus(t.Context(), nil, "key", displayStatus{Text: "Yes"})
	if literal.Text != "Yes" || literal.Unavailable {
		t.Fatalf("literal changed: %+v", literal)
	}
	incomplete := e.formatItemStatus(t.Context(), nil, "key", displayStatus{Text: "broken ${vault."})
	if !incomplete.Unavailable || incomplete.Text != "Vault text unavailable" {
		t.Fatalf("incomplete token displayed: %+v", incomplete)
	}
}
