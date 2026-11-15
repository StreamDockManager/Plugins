package catalog

import (
	"bytes"
	"encoding/json"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"os"
	"testing"
)

// TestSettingsRegistryCoversEveryOfficialPlugin verifies settings registry covers every official plugin
func TestSettingsRegistryCoversEveryOfficialPlugin(t *testing.T) {
	if len(settingsRegistry) != len(Definitions) {
		t.Fatalf("registry has %d plugins, catalog has %d", len(settingsRegistry), len(Definitions))
	}
	for _, d := range Definitions {
		if _, ok := settingsRegistry[d.Slug]; !ok {
			t.Errorf("missing %s", d.Slug)
		}
		for _, a := range d.Actions {
			seen := map[string]bool{}
			for _, f := range ActionFields(d.Slug, a) {
				if f.Key == "" || seen[f.Key] {
					t.Errorf("%s/%s duplicate or empty key %q", d.Slug, a, f.Key)
				}
				seen[f.Key] = true
			}
		}
	}
	if PluginFields("unknown") != nil || ActionFields("unknown", "unknown") != nil {
		t.Fatal("unknown plugin gained settings")
	}
}

// TestCorePluginSettingsRemainFreshPerRequest verifies core plugin settings remain fresh per request
func TestCorePluginSettingsRemainFreshPerRequest(t *testing.T) {
	for _, slug := range []string{"weather", "steam-library", "internet-speed-test"} {
		first := PluginFields(slug)
		key := first[0].Key
		first[0].Key = "edited fixture"
		if PluginFields(slug)[0].Key != key {
			t.Fatalf("%s leaked caller edits into metadata", slug)
		}
	}
}

// TestSettingsModulesMatchCharacterizedOutput verifies settings modules match characterized output
func TestSettingsModulesMatchCharacterizedOutput(t *testing.T) {
	expected, err := os.ReadFile("testdata/settings-modules.json")
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string]any{}
	for _, d := range Definitions {
		modules[d.Slug] = SettingsModule(d)
	}
	actual, err := json.MarshalIndent(modules, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Compare the JSON contract independently of source-fixture indentation
	var expectedJSON, actualJSON bytes.Buffer
	if err := json.Compact(&expectedJSON, expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&actualJSON, actual); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expectedJSON.Bytes(), actualJSON.Bytes()) {
		t.Fatal("settings module contract changed")
	}
}

// TestAllSettingsFactoriesOwnNestedMetadata verifies all settings factories own nested metadata
func TestAllSettingsFactoriesOwnNestedMetadata(t *testing.T) {
	for _, d := range Definitions {
		for _, action := range append([]string{""}, d.Actions...) {
			get := func() []Field {
				if action == "" {
					return PluginFields(d.Slug)
				}
				return ActionFields(d.Slug, action)
			}
			before, _ := json.Marshal(get())
			changed := get()
			for i := range changed {
				f := &changed[i]
				f.Key = "changed"
				if f.Min != nil {
					*f.Min = 123
				}
				if f.Max != nil {
					*f.Max = 456
				}
				if len(f.Options) > 0 {
					f.Options[0] = "changed"
				}
				for k := range f.OptionLabels {
					f.OptionLabels[k] = "changed"
				}
			}
			after, _ := json.Marshal(get())
			if !bytes.Equal(before, after) {
				t.Fatalf("%s/%s leaked nested mutation", d.Slug, action)
			}
		}
	}
}

// TestRegistrationRejectsIncompleteAndDuplicateEntries verifies registration rejects incomplete and duplicate entries
func TestRegistrationRejectsIncompleteAndDuplicateEntries(t *testing.T) {
	for _, entries := range [][]engine.Registration{{{}}, {registrations[0], registrations[0]}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid registrations accepted")
				}
			}()
			registrationIndex(entries)
		}()
	}
}
