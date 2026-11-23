package retroachievements

import (
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type setupSlot struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	EntryKind string `json:"entry_kind"`
	Mode      string `json:"mode"`
	Required  bool   `json:"required"`
	Purpose   string `json:"purpose"`
	Field     string `json:"field"`
	Action    string `json:"action"`
}

type setupSlots struct {
	Version int         `json:"version"`
	Slots   []setupSlot `json:"slots"`
}

// Declare the same logical account slots with exact scopes for each action
func SetupSlots() setupSlots {
	slots := setupSlots{Version: 1, Slots: make([]setupSlot, 0, len(Definition.Actions)*2)}
	for _, slug := range Definition.Actions {
		action := engine.ID(Definition.Slug) + "." + slug
		slots.Slots = append(slots.Slots,
			setupSlot{ID: "api_key", Kind: "secret", EntryKind: "api-key", Mode: "direct", Field: "api_key", Action: action, Purpose: "Send your Web API key directly to retroachievements.org to read your achievement data"},
			setupSlot{ID: "username", Kind: "value", EntryKind: "text", Mode: "direct", Field: "username", Action: action, Purpose: "Use your RetroAchievements username or ULID to select your account"})
	}
	return slots
}

// PackagePermissions discloses direct secret access independently of setup slots.
func PackagePermissions() *sdk.PermissionDeclaration {
	actions := map[string]sdk.ActionPermissions{}
	for _, slot := range SetupSlots().Slots {
		if slot.Kind == "secret" {
			actions[slot.Action] = sdk.ActionPermissions{Purpose: slot.Purpose, Optional: []map[string]string{
				{"id": "vault.read-secret", "field": slot.Field, "entry_kind": slot.EntryKind, "purpose": slot.Purpose},
			}}
		}
	}
	return &sdk.PermissionDeclaration{Version: 1, Actions: actions}
}
