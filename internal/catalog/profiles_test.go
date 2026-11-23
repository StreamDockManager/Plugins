package catalog

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// suggestedActionLeaves collects leaf actions from the suggested profile fixture
func suggestedActionLeaves(t *testing.T, action map[string]any) []map[string]any {
	t.Helper()
	if action["kind"] != "dial" {
		return []map[string]any{action}
	}
	lanes, ok := action["actions"].([]any)
	if !ok || len(lanes) != 3 {
		t.Fatal("dial must include left, right, and press actions")
	}
	leaves := make([]map[string]any, len(lanes))
	for index, lane := range lanes {
		leaves[index], ok = lane.(map[string]any)
		if !ok || leaves[index]["kind"] != "plugin" {
			t.Fatal("generated dial lane must retain its plugin action")
		}
		if index == 2 && leaves[0]["action_id"] == ID("spotify")+".volume" {
			if leaves[index]["action_id"] != ID("spotify")+".mute-toggle" {
				t.Fatal("volume press must toggle mute")
			}
			continue
		}
		if !reflect.DeepEqual(lane, lanes[0]) {
			t.Fatal("plugin dial lanes must keep the same settings; rotation direction comes from signed input")
		}
	}
	return leaves
}

// TestSuggestedProfilesIncludeCurrentWorkspaceEnvelope verifies suggested profiles include current workspace envelope
func TestSuggestedProfilesIncludeCurrentWorkspaceEnvelope(t *testing.T) {
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			files, err := SuggestedProfiles(definition)
			if err != nil {
				t.Fatal(err)
			}
			for name, data := range files {
				var document map[string]any
				if err := json.Unmarshal(data, &document); err != nil {
					t.Fatal(err)
				}
				for key, expected := range map[string]any{
					"schema_version":         float64(1),
					"minimum_manager":        definition.MinimumManager,
					"configuration_revision": float64(0),
					"saved_macros":           []any{},
					"rules":                  []any{},
					"history":                map[string]any{"undo": []any{}, "redo": []any{}},
				} {
					if !reflect.DeepEqual(document[key], expected) {
						t.Errorf("%s: required workspace field %s = %#v; want %#v", name, key, document[key], expected)
					}
				}
				profiles, ok := document["profiles"].([]any)
				if !ok || len(profiles) != 1 || document["active_profile"] != profiles[0].(map[string]any)["id"] {
					t.Errorf("%s: missing current active profile", name)
				}
			}
		})
	}
}

// TestFamilyVariantsKeepEquivalentSuggestedPlacements verifies family variants keep equivalent suggested placements
func TestFamilyVariantsKeepEquivalentSuggestedPlacements(t *testing.T) {
	var hardware struct {
		Models []profileModel `json:"models"`
	}
	if err := json.Unmarshal(profileHardware, &hardware); err != nil {
		t.Fatal(err)
	}
	for _, definition := range Definitions {
		files, err := SuggestedProfiles(definition)
		if err != nil {
			t.Fatal(err)
		}
		normalize := func(id string) any {
			var document struct {
				Profiles []struct {
					Pages []suggestedPage `json:"pages"`
				} `json:"profiles"`
				Unavailable []string `json:"unavailable_actions"`
			}
			if err := json.Unmarshal(files["profiles/"+id+".json"], &document); err != nil {
				t.Fatal(err)
			}
			pages := document.Profiles[0].Pages
			indexes := map[string]int{}
			for i, page := range pages {
				indexes[page.ID] = i
			}
			for i := range pages {
				pages[i].ID = ""
				for _, raw := range pages[i].Bindings {
					action := raw.(map[string]any)["action"].(map[string]any)
					if action["kind"] == "page" {
						action["page_id"] = indexes[action["page_id"].(string)]
					}
				}
			}
			return document
		}
		for _, model := range hardware.Models {
			if model.Family != "" && !reflect.DeepEqual(normalize(model.ID), normalize(model.Family)) {
				t.Fatalf("%s: %s differs from family %s", definition.Slug, model.ID, model.Family)
			}
		}
	}
}

// TestSpotifyDialOnlyControlsAndCompanionPlacement verifies spotify dial only controls and companion placement
func TestSpotifyDialOnlyControlsAndCompanionPlacement(t *testing.T) {
	var definition Definition
	for _, d := range Definitions {
		if d.Slug == "spotify" {
			definition = d
		}
	}
	caps := Descriptor(definition)
	for _, a := range []string{"volume", "seek"} {
		if !slices.Equal(caps.Actions[ID("spotify")+"."+a].ControlTypes, []string{"encoder"}) {
			t.Fatal(a)
		}
	}
	files, err := SuggestedProfiles(definition)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Profiles []struct {
			Pages []suggestedPage `json:"pages"`
		} `json:"profiles"`
		Unavailable []string `json:"unavailable_actions"`
	}
	if err = json.Unmarshal(files["profiles/n4-pro.json"], &document); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"volume-display", "progress"} {
		found := false
		for _, page := range document.Profiles[0].Pages {
			for control, raw := range page.Bindings {
				binding := raw.(map[string]any)
				action := binding["action"].(map[string]any)
				if action["action_id"] == ID("spotify")+"."+a {
					if !strings.HasPrefix(control, "touch-") {
						t.Fatal("companion not on touch", control, a)
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("missing companion", a)
		}
	}
	document.Profiles = nil
	if err = json.Unmarshal(files["profiles/mini.json"], &document); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"volume", "seek"} {
		if !slices.Contains(document.Unavailable, ID("spotify")+"."+a) {
			t.Fatal("dial action not unavailable", a)
		}
	}
}

// TestSuggestedProfilesCoverHardwareAndCompatibleActions verifies suggested profiles cover hardware and compatible actions
func TestSuggestedProfilesCoverHardwareAndCompatibleActions(t *testing.T) {
	var hardware struct {
		Models []profileModel `json:"models"`
	}
	if err := json.Unmarshal(profileHardware, &hardware); err != nil {
		t.Fatal(err)
	}
	for _, d := range Definitions {
		t.Run(d.Slug, func(t *testing.T) {
			files, err := SuggestedProfiles(d)
			if err != nil {
				t.Fatal(err)
			}
			repeated, err := SuggestedProfiles(d)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(hardware.Models) {
				t.Fatal("missing hardware profiles")
			}
			for _, model := range hardware.Models {
				key := "profiles/" + model.ID + ".json"
				if string(files[key]) != string(repeated[key]) {
					t.Fatal("nondeterministic profile")
				}
				var document struct {
					Version  int `json:"schema_version"`
					Profiles []struct {
						Model string          `json:"model_id"`
						Pages []suggestedPage `json:"pages"`
					} `json:"profiles"`
				}
				if err := json.Unmarshal(files[key], &document); err != nil {
					t.Fatal(err)
				}
				if document.Version != 1 || len(document.Profiles) != 1 || document.Profiles[0].Model != model.ID {
					t.Fatal("invalid model/profile contract")
				}
				controls := map[string]profileControl{}
				for _, c := range model.Controls {
					controls[c.ID] = c
				}
				pages := map[string]bool{}
				for _, p := range document.Profiles[0].Pages {
					pages[p.ID] = true
				}
				found := map[string]bool{}
				descriptor := Descriptor(d)
				for _, p := range document.Profiles[0].Pages {
					for id, raw := range p.Bindings {
						control, ok := controls[id]
						if !ok {
							t.Fatalf("unknown %s control %s", model.ID, id)
						}
						action := raw.(map[string]any)["action"].(map[string]any)
						if (control.Kind == "encoder") != (action["kind"] == "dial") {
							t.Fatal("encoder placement needs explicit dial actions; other controls must not use dials")
						}
						if action["kind"] == "page" {
							if !pages[action["page_id"].(string)] {
								t.Fatal("missing navigation target")
							}
							continue
						}
						for _, leaf := range suggestedActionLeaves(t, action) {
							actionID := leaf["action_id"].(string)
							capability, ok := descriptor.Actions[actionID]
							if !ok || !slices.Contains(capability.ControlTypes, control.Kind) {
								t.Fatal("incompatible action placement")
							}
							found[actionID] = true
						}
					}
				}
				for actionID, capability := range descriptor.Actions {
					compatible := slices.ContainsFunc(model.Controls, func(c profileControl) bool { return slices.Contains(capability.ControlTypes, c.Kind) })
					if compatible != found[actionID] {
						t.Fatalf("coverage mismatch %s on %s", actionID, model.ID)
					}
				}
			}
		})
	}
}

// TestSuggestedProfilesMaterializeCurrentRefreshDefaults verifies suggested profiles materialize current refresh defaults
func TestSuggestedProfilesMaterializeCurrentRefreshDefaults(t *testing.T) {
	for _, definition := range Definitions {
		files, err := SuggestedProfiles(definition)
		if err != nil {
			t.Fatal(err)
		}
		descriptor := Descriptor(definition)
		for name, data := range files {
			var document struct {
				Profiles []struct {
					Pages []suggestedPage `json:"pages"`
				} `json:"profiles"`
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			for _, profile := range document.Profiles {
				for _, page := range profile.Pages {
					for _, raw := range page.Bindings {
						action := raw.(map[string]any)["action"].(map[string]any)
						for _, leaf := range suggestedActionLeaves(t, action) {
							if leaf["kind"] != "plugin" {
								continue
							}
							capability := descriptor.Actions[leaf["action_id"].(string)]
							if capability.Refresh == nil {
								continue
							}
							settings := leaf["settings"].(map[string]any)
							if settings["update_interval_ms"] != float64(capability.Refresh.DefaultMS) {
								t.Fatal("suggested binding lacks explicit refresh default", name, leaf)
							}
							if _, retired := settings["interval"]; retired {
								t.Fatal("suggestion emitted retired interval")
							}
						}
					}
				}
			}
		}
	}
}

// TestPollingInspectorDefaultsAreExplicitCurrentSettings verifies polling inspector defaults are explicit current settings
func TestPollingInspectorDefaultsAreExplicitCurrentSettings(t *testing.T) {
	for _, slug := range []string{"http-webhooks", "service-monitor"} {
		for _, definition := range Definitions {
			if definition.Slug != slug {
				continue
			}
			descriptor := Descriptor(definition)
			for _, action := range definition.Actions {
				policy := descriptor.Actions[ID(slug)+"."+action].Refresh
				if policy == nil {
					continue
				}
				settings := fieldDefaults(ActionFields(slug, action))
				if settings["update_interval_ms"] != float64(policy.DefaultMS) {
					t.Fatal("inspector omits explicit current default", slug, action, settings)
				}
				if _, err := policy.Resolve(settings); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestSpotifyVolumeMutePressAcrossDialModels(t *testing.T) {
	var definition Definition
	for _, d := range Definitions {
		if d.Slug == "spotify" {
			definition = d
		}
	}
	files, err := SuggestedProfiles(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"k1-pro", "m3", "n1", "n1-vsd", "n3", "n3-vsd", "n4", "n4-pro"} {
		var doc struct {
			Profiles []struct {
				Pages []suggestedPage `json:"pages"`
			} `json:"profiles"`
		}
		if err = json.Unmarshal(files["profiles/"+model+".json"], &doc); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, page := range doc.Profiles[0].Pages {
			for _, raw := range page.Bindings {
				a := raw.(map[string]any)["action"].(map[string]any)
				if a["kind"] != "dial" {
					continue
				}
				lanes := a["actions"].([]any)
				if lanes[0].(map[string]any)["action_id"] != ID("spotify")+".volume" {
					continue
				}
				count++
				if !reflect.DeepEqual(lanes[0], lanes[1]) {
					t.Fatal(model, "rotation lanes changed")
				}
				if lanes[2].(map[string]any)["action_id"] != ID("spotify")+".mute-toggle" {
					t.Fatal(model, "missing mute press")
				}
			}
		}
		if count != 1 {
			t.Fatal(model, "volume placements", count)
		}
	}
}
