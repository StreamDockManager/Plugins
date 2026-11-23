package settings

import (
	"context"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// VaultField describes one optional action-scoped input. The marker is a plain
// settings choice; only Manager can authorize and resolve the corresponding slot.
type VaultField struct{ Field, EntryKind, Purpose string }

func VaultFields(slug, action string) []VaultField {
	switch slug {
	case "weather":
		return []VaultField{{"location", "location", "Share the saved latitude and longitude with Weather to request conditions, forecasts, or sun times from your configured service."}}
	case "home-assistant":
		return []VaultField{{"token", "bearer-token", "Authenticate to the configured Home Assistant instance."}}
	case "obs-studio":
		return []VaultField{{"password", "credential", "Authenticate to the configured OBS WebSocket server."}}
	case "mqtt":
		return []VaultField{{"username", "text", "Identify the account on the configured MQTT broker."}, {"password", "credential", "Authenticate to the configured MQTT broker."}}
	case "vlc":
		return []VaultField{{"password", "credential", "Authenticate to the configured VLC HTTP interface."}}
	case "make":
		if action == "webhook" {
			return []VaultField{{"webhook", "credential", "Send the configured payload to the selected secret Make webhook URL."}}
		}
		return []VaultField{{"token", "api-key", "Authenticate to the selected Make API region."}}
	case "http-webhooks":
		if action == "request" || action == "status" || action == "duration" {
			return []VaultField{{"webhook", "credential", "Send the configured request to the selected secret webhook URL."}}
		}
	case "service-monitor":
		if action == "http" {
			return []VaultField{{"url", "credential", "Check the selected private service URL and show only its HTTP status and latency."}}
		}
	case "steam-library":
		if action == "playtime" {
			return []VaultField{{"account", "text", "Read playtime from the selected account in the local Steam library."}}
		}
	case "osrs-hiscores", "runescape-hiscores":
		return []VaultField{{"username", "text", "Look up the selected player on the RuneScape hiscores service."}}
	}
	return nil
}

type VaultSlot struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	EntryKind string `json:"entry_kind"`
	Mode      string `json:"mode"`
	Field     string `json:"field"`
	Action    string `json:"action"`
	Purpose   string `json:"purpose"`
	Required  bool   `json:"required"`
}
type VaultSlots struct {
	Version int         `json:"version"`
	Slots   []VaultSlot `json:"slots"`
}

func VaultDeclarations(slug, pluginID string, actions []string) (*VaultSlots, *sdk.PermissionDeclaration) {
	slots := &VaultSlots{Version: 1}
	permissions := &sdk.PermissionDeclaration{Version: 1, Actions: map[string]sdk.ActionPermissions{}}
	for _, action := range actions {
		uuid := pluginID + "." + action
		for _, field := range VaultFields(slug, action) {
			kind := "value"
			if field.EntryKind != "text" {
				kind = "secret"
				if field.EntryKind == "location" {
					kind = "location"
				}
				group := permissions.Actions[uuid]
				group.Purpose = field.Purpose
				group.Optional = append(group.Optional, map[string]string{"id": "vault.read-" + kind, "field": field.Field, "entry_kind": field.EntryKind, "purpose": field.Purpose})
				permissions.Actions[uuid] = group
			}
			slots.Slots = append(slots.Slots, VaultSlot{ID: field.Field, Kind: kind, EntryKind: field.EntryKind, Mode: "direct", Field: field.Field, Action: uuid, Purpose: field.Purpose})
		}
	}
	if len(slots.Slots) == 0 {
		slots = nil
	}
	if len(permissions.Actions) == 0 {
		permissions = nil
	}
	return slots, permissions
}

// URLCredential requests a fresh action-specific grant before a private HTTP request.
func URLCredential(ctx context.Context, client *sdk.Client, slug, uuid, action, field string) (*sdk.VaultSecret, error) {
	if client == nil {
		return nil, &sdk.RPCError{Code: "operation_unavailable", Message: "Vault requires an authorized Manager connection"}
	}
	for _, candidate := range VaultFields(slug, action) {
		if candidate.Field != field || candidate.EntryKind != "credential" {
			continue
		}
		value, err := client.ReadVaultSecret(ctx, sdk.VaultValueRequest{Action: uuid, Slot: field, Field: field})
		if err != nil {
			return nil, err
		}
		if value.EntryKind != "credential" {
			break
		}
		return &value, nil
	}
	return nil, &sdk.RPCError{Code: "not_declared", Message: "Choose a Vault URL credential for this action"}
}
