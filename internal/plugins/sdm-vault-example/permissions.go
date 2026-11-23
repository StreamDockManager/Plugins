package vaultexample

import (
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type scope = map[string]string

const locationPurpose = "Read the selected latitude and longitude to verify location sharing."
const summaryPurpose = "Review clipboard text with a selected credential at Example Summarizer."

func actionID(a string) string { return engine.ID(Slug) + "." + a }
func kindField(kind string) string {
	switch kind {
	case "api-key":
		return "api_key"
	case "bearer-token":
		return "bearer_token"
	case "credential", "text":
		return kind
	}
	return ""
}

var secretKinds = []string{"api-key", "bearer-token", "credential"}

func secretScope(op, kind string) scope {
	purpose := "Check that the selected example credential can be read."
	if op == "write" {
		purpose = "Create or rotate a generated example credential in this plugin's private slot."
	}
	return scope{"id": "vault." + op + "-secret", "field": kindField(kind), "entry_kind": kind, "purpose": purpose}
}
func summaryAccess(mode string) scope {
	return scope{"id": "vault.access", "mode": mode, "field": "authorization", "operation": "authorize-request", "origin": "https://api.example.test", "recipe": "clipboard-summary"}
}
func network(a, transport string) scope {
	origin := WebhookOrigin
	if transport == "http" {
		origin = "http://" + viewerAddress
	}
	return scope{"id": "network." + transport, "origin": origin, "recipe": a + "-webhook", "data_class": "example.ping"}
}
func summaryScopes() []scope {
	use := summaryAccess("use-entry")
	return []scope{use, {"id": "network.https", "origin": use["origin"], "recipe": use["recipe"], "data_class": "clipboard.text"},
		{"id": "ui.popup", "mode": "text-result"}, {"id": "clipboard.read-text", "mode": "manager-recipe"}}
}
func PackagePermissions() *sdk.PermissionDeclaration {
	actions := map[string]sdk.ActionPermissions{}
	for _, op := range []string{"read", "write"} {
		var scopes []scope
		for _, k := range secretKinds {
			scopes = append(scopes, secretScope(op, k))
		}
		actions[actionID(op)] = sdk.ActionPermissions{Purpose: scopes[0]["purpose"], Optional: scopes}
	}
	bearer := summaryAccess("use-entry")
	bearer["field"] = "bearer_authorization"
	actions[actionID("summary")] = sdk.ActionPermissions{Purpose: summaryPurpose, Optional: append(summaryScopes(),
		summaryAccess("browse-metadata"), scope{"id": "clipboard.write-text", "mode": "explicit-copy"}, bearer)}
	for _, sender := range []string{"plugin", "manager"} {
		scopes := []scope{network(sender, "https"), network(sender, "http")}
		if sender == "manager" {
			scopes = append(scopes, scope{"id": "ui.popup", "mode": "text-result"})
		}
		actions[actionID(sender+"-post")] = sdk.ActionPermissions{Purpose: "Send a fixed example ping to the local webhook viewer.", Optional: scopes}
	}
	actions[actionID("location")] = sdk.ActionPermissions{Purpose: locationPurpose, Optional: []scope{{"id": "vault.read-location", "entry_kind": "location", "field": "location", "purpose": locationPurpose}}}
	return &sdk.PermissionDeclaration{Version: 1, Actions: actions}
}
func SetupSlots() map[string]any {
	slot := func(id, kind, entry, mode, field, a, why string) map[string]any {
		return map[string]any{"id": id, "kind": kind, "entry_kind": entry, "mode": mode, "field": field, "action": actionID(a), "required": false, "purpose": why}
	}
	slots := []map[string]any{slot("text", "value", "text", "direct", "text", "read", "Check that the selected text variable can be read.")}
	for _, k := range secretKinds {
		s := secretScope("read", k)
		slots = append(slots, slot(s["field"], "secret", k, "direct", s["field"], "read", s["purpose"]))
	}
	use := summaryAccess("use-entry")
	for _, kind := range []string{"api-key", "bearer-token"} {
		field := use["field"]
		if kind == "bearer-token" {
			field = "bearer_authorization"
		}
		item := slot("summary_"+kindField(kind), "secret", kind, "broker", field, "summary", summaryPurpose)
		for _, key := range []string{"origin", "recipe", "operation"} {
			item[key] = use[key]
		}
		slots = append(slots, item)
	}
	slots = append(slots, slot("location", "location", "location", "direct", "location", "location", locationPurpose))
	return map[string]any{"version": 1, "slots": slots}
}
