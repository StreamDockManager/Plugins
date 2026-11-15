package engine

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

const DiagnosticLimit = 256 * 1024

// SendDiagnostic preserves the SDK Message adapter's empty payload on encoding
// failure. Serialize once so the bytes checked are the bytes actually delivered,
// including for payloads with a custom marshaler. Callers retain workflow order
func SendDiagnostic(ctx context.Context, c *sdk.Client, destination string, value any) error {
	if destination == "" || value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	if len(data) > DiagnosticLimit {
		data, _ = json.Marshal(map[string]any{"message": "Preview exceeds 256 KiB; narrow the selected fields", "truncated": true})
	}
	return c.Send(ctx, sdk.Event{Event: "sendToPropertyInspector", Context: destination, Payload: data})
}
