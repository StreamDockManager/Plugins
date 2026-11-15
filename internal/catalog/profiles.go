package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed profile-hardware.v1.json
var profileHardware []byte

type profileControl struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Semantics []string `json:"semantics"`
}
type profileModel struct {
	ID       string           `json:"model_id"`
	Name     string           `json:"name"`
	Family   string           `json:"profile_family,omitempty"`
	Controls []profileControl `json:"controls"`
}
type suggestedPage struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Bindings map[string]any `json:"bindings"`
}

// fieldDefaults collects defaults from the declared setting fields
func fieldDefaults(fields []Field) map[string]any {
	out := map[string]any{}
	for _, field := range fields {
		if field.Default != nil {
			out[field.Key] = field.Default
		}
	}
	return out
}

// New suggestions materialize the current advertised refresh default. This is
// producer data, not permission to upgrade an existing binding at runtime
func actionDefaults(d Definition, action string) map[string]any {
	values := fieldDefaults(ActionFields(d.Slug, action))
	if policy := Descriptor(d).Actions[ID(d.Slug)+"."+action].Refresh; policy != nil {
		values["update_interval_ms"] = policy.DefaultMS
	}
	return values
}

// SuggestedProfiles uses the same action declarations/defaults as the packaged inspectors
// The hardware snapshot is synchronized explicitly; standalone builds need no Manager checkout
func SuggestedProfiles(d Definition) (map[string][]byte, error) {
	var hardware struct {
		Version int            `json:"schema_version"`
		Models  []profileModel `json:"models"`
	}
	if err := json.Unmarshal(profileHardware, &hardware); err != nil {
		return nil, err
	}
	if hardware.Version != 1 {
		return nil, fmt.Errorf("unsupported profile hardware version")
	}
	descriptor := Descriptor(d)
	owner := ID(d.Slug)
	output := map[string][]byte{}
	defaults := map[string]any{}
	for _, action := range d.Actions {
		values := actionDefaults(d, action)
		if _, ok := values["shared_id"]; ok {
			values["shared_id"] = owner
		}
		if len(values) > 0 {
			defaults[owner+"."+action] = values
		}
	}
	for _, model := range hardware.Models {
		buttons := []profileControl{}
		for _, c := range model.Controls {
			if c.Kind == "button" {
				buttons = append(buttons, c)
			}
		}
		compatible := []string{}
		unavailable := []string{}
		for _, action := range d.Actions {
			types := descriptor.Actions[owner+"."+action].ControlTypes
			if slices.ContainsFunc(model.Controls, func(c profileControl) bool { return slices.Contains(types, c.Kind) }) {
				compatible = append(compatible, action)
			} else {
				unavailable = append(unavailable, owner+"."+action)
			}
		}
		allocate := func(navigation bool) ([]suggestedPage, error) {
			reserved := map[string]bool{}
			if navigation {
				if len(buttons) < 2 {
					return nil, fmt.Errorf("multiple profile pages require navigation controls")
				}
				for _, c := range buttons[len(buttons)-2:] {
					reserved[c.ID] = true
				}
			}
			pages := []suggestedPage{}
			newPage := func() {
				number := len(pages) + 1
				name := d.Name
				if number > 1 {
					name = fmt.Sprintf("%s %d", d.Name, number)
				}
				pages = append(pages, suggestedPage{ID: fmt.Sprintf("%s-%s-%d", owner, model.ID, number), Name: name, Bindings: map[string]any{}})
			}
			newPage()
			for _, action := range compatible {
				capability := descriptor.Actions[owner+"."+action]
				preference := []string{"button", "touch", "encoder"}
				if capability.AdditiveDial {
					preference = []string{"encoder", "button", "touch"}
				}
				if d.Slug == "spotify" && slices.Contains([]string{"volume-display", "progress", "track-info"}, action) {
					preference = []string{"touch", "button"}
				}
				find := func() string {
					for _, kind := range preference {
						if !slices.Contains(capability.ControlTypes, kind) {
							continue
						}
						for _, c := range model.Controls {
							if c.Kind == kind && !reserved[c.ID] && pages[len(pages)-1].Bindings[c.ID] == nil {
								return c.ID
							}
						}
					}
					return ""
				}
				control := find()
				if control == "" {
					if !navigation {
						return nil, nil
					}
					newPage()
					control = find()
				}
				if control == "" {
					return nil, fmt.Errorf("no compatible control for %s on %s", action, model.ID)
				}
				name := ActionName(d.Slug, action)
				settings := actionDefaults(d, action)
				if values, ok := defaults[owner+"."+action]; ok {
					settings = values.(map[string]any)
				}
				leaf := func() map[string]any {
					return map[string]any{"kind": "plugin", "plugin_id": owner, "action_id": owner + "." + action, "settings": settings}
				}
				binding := leaf()
				if slices.ContainsFunc(model.Controls, func(c profileControl) bool { return c.ID == control && c.Kind == "encoder" }) {
					// Manager preserves signed rotation for each plugin lane, so the
					// same settings retain left/right direction and press behavior
					binding = map[string]any{"kind": "dial", "actions": []any{leaf(), leaf(), leaf()}}
				}
				pages[len(pages)-1].Bindings[control] = map[string]any{"title": name, "color": "#00000000", "action": binding}
			}
			if len(pages) > 1 {
				for i := range pages {
					for offset, title := range []string{"Previous page", "Next page"} {
						target := (i + len(pages) - 1) % len(pages)
						if offset == 1 {
							target = (i + 1) % len(pages)
						}
						pages[i].Bindings[buttons[len(buttons)-2+offset].ID] = map[string]any{"title": title, "color": "#00000000", "action": map[string]any{"kind": "page", "page_id": pages[target].ID}}
					}
				}
			}
			return pages, nil
		}
		pages, err := allocate(false)
		if err != nil {
			return nil, err
		}
		if pages == nil {
			pages, err = allocate(true)
			if err != nil {
				return nil, err
			}
		}
		profileID := owner + "-" + model.ID
		// Suggested layouts carry the complete current workspace envelope, even
		// when they have no macros, rules, revisions, or history to import
		document := map[string]any{
			"schema_version": 1, "minimum_manager": "0.1.0",
			"configuration_revision": 0, "saved_macros": []any{}, "rules": []any{},
			"history":              map[string]any{"undo": []any{}, "redo": []any{}},
			"active_profile":       profileID,
			"profiles":             []any{map[string]any{"id": profileID, "name": d.Name + " · " + model.Name, "model_id": model.ID, "active_page": pages[0].ID, "pages": pages}},
			"unavailable_actions":  unavailable,
			"plugin_configuration": map[string]any{"settings": fieldDefaults(PluginFields(d.Slug)), "action_defaults": defaults},
		}
		bytes, err := json.MarshalIndent(document, "", "    ")
		if err != nil {
			return nil, err
		}
		output["profiles/"+model.ID+".json"] = append(bytes, '\n')
	}
	return output, nil
}
