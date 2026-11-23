package catalog

import (
	"encoding/json"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"testing"
)

// Verify the catalog declares exactly the existing actions and no Vault startup requirement
func TestRetroAchievementsPackageSetupSlots(t *testing.T) {
	for _, d := range Definitions {
		encoded, err := json.Marshal(PackageSetupSlots(d.Slug))
		if err != nil {
			t.Fatal(err)
		}
		if d.Slug == "sdm-vault-example" || d.Slug == "randomizer-dice" {
			if string(encoded) == "null" {
				t.Fatalf("missing setup slots for %s", d.Slug)
			}
			continue
		}
		if d.Slug != "retroachievements" {
			slots, _ := metadata.VaultDeclarations(d.Slug, ID(d.Slug), d.Actions)
			if slots != nil {
				if string(encoded) == "null" {
					t.Fatalf("missing slots for %s", d.Slug)
				}
				continue
			}
			if string(encoded) != "null" {
				t.Fatalf("unexpected Vault setup for %s", d.Slug)
			}
			continue
		}
		var contract struct {
			Version int `json:"version"`
			Slots   []struct {
				ID, Kind, Mode, Field, Action, Purpose string
				Required                               bool
				EntryKind                              string `json:"entry_kind"`
			}
		}
		if err = json.Unmarshal(encoded, &contract); err != nil || contract.Version != 1 || len(contract.Slots) != 2*len(d.Actions) {
			t.Fatal("incorrect Vault setup contract", err)
		}
		seen := map[string]bool{}
		for _, slot := range contract.Slots {
			key := slot.ID + "/" + slot.Action
			if seen[key] || slot.Purpose == "" || slot.Required || slot.Field != slot.ID || slot.Mode != "direct" {
				t.Fatal("invalid setup slot", slot)
			}
			seen[key] = true
			if slot.ID == "api_key" && (slot.Kind != "secret" || slot.EntryKind != "api-key") || slot.ID == "username" && (slot.Kind != "value" || slot.EntryKind != "text") {
				t.Fatal("wrong type", slot)
			}
		}
		for _, action := range d.Actions {
			for _, id := range []string{"api_key", "username"} {
				if !seen[id+"/"+ID(d.Slug)+"."+action] {
					t.Fatal("missing exact action scope", id, action)
				}
			}
		}
		if len(PluginFields(d.Slug)) != 2 || len(ActionFields(d.Slug, "completion")) != 3 {
			t.Fatal("portable setup or action controls lost")
		}
	}
}
