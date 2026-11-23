package randomizerdice

import (
	"context"
	"strings"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

const itemText = "item_text"

// SetupSlots lets a user deliberately link an unprotected Text entry per action
func SetupSlots() map[string]any {
	var slots []map[string]any
	for _, name := range []string{"list", "draw"} {
		slots = append(slots, map[string]any{
			"id": itemText, "kind": "value", "entry_kind": "text", "mode": "direct",
			"field": itemText, "action": engine.ID(Definition.Slug) + "." + name, "required": false,
			"purpose": "Allow the selected nonsecret Vault text to appear on the device and its preview when a result references it",
		})
	}
	return map[string]any{"version": 1, "slots": slots}
}

// Resolve only the chosen item at display time, never the input list or saved draw history
func (e *Plugin) formatItemStatus(ctx context.Context, c *sdk.Client, id string, status displayStatus) displayStatus {
	b, ok := e.Bindings[id]
	if !ok || !strings.Contains(status.Text, "${vault.") {
		return status
	}
	switch action(b.Action) {
	case "list", "draw":
		// Keep identifiers and the original configured list out of the formatting request
		b.Settings = nil
		_, tail, _ := strings.Cut(status.Text, "${vault.")
		if strings.Contains(tail, "}") {
			requestCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			defer cancel()
			text, err := engine.FormatSettingText(requestCtx, c, b, itemText, status.Text)
			if err == nil {
				status.Text = text
				return status
			}
		}
		status.Text = "Vault text unavailable"
		status.Unavailable = true
	}
	return status
}

// Identify bindings whose saved result must be rechecked before a cached display is replayed
func (e *Plugin) isVaultItemResult(id string, b binding) bool {
	return (action(b.Action) == "list" || action(b.Action) == "draw") &&
		strings.Contains(e.lookupState(id, b.Settings).Color, "${vault.")
}

// Clear previous formatted frames before the shared renderer replays themed non-live views
func (e *Plugin) clearVaultItemDisplays() {
	for id, b := range e.Bindings {
		if e.isVaultItemResult(id, b) {
			delete(e.Last, id)
			delete(e.LastStatus, id)
		}
	}
}

// Replace displayed references on explicit theme or permission changes without polling literals
func (e *Plugin) refreshVaultItemDisplays(ctx context.Context, c *sdk.Client) error {
	e.clearVaultItemDisplays()
	// These notices have no triggering device binding; authorize only the reference views explicitly
	refresh, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	for id, b := range e.Bindings {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.isVaultItemResult(id, b) {
			if err := e.Render(refresh, c, id); err != nil {
				return err
			}
		}
	}
	return nil
}
