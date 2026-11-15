package catalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"testing"
)

// TestMetadataCanonicalBytes compares serialized metadata with its characterized canonical bytes
func TestMetadataCanonicalBytes(t *testing.T) {
	records := []any{}
	for _, d := range Definitions {
		visuals := map[string]ActionVisual{}
		fields := map[string][]Field{}
		for _, a := range d.Actions {
			visuals[a] = VisualFor(d.Slug, a)
			fields[a] = ActionFields(d.Slug, a)
		}
		records = append(records, []any{Descriptor(d), PluginFields(d.Slug), fields, visuals, PluginAssets(d)})
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	const expected = "c7119ea8ecdebe8a6336fc9a10071542327326a01a657ce5fb65b503d6a3a9f6"
	if digest != expected {
		t.Fatalf("canonical metadata changed: %s", digest)
	}
}

// TestDescriptorBuildsActionFieldsOnce verifies descriptor builds action fields once
func TestDescriptorBuildsActionFieldsOnce(t *testing.T) {
	const slug = "descriptor-count-fixture"
	calls := 0
	settingsRegistry[slug] = engine.Registration{ActionFields: func(string) []Field {
		calls++
		return []Field{metadata.Text("first", "First", ""), metadata.Text("second", "Second", "")}
	}}
	defer delete(settingsRegistry, slug)
	descriptor := Descriptor(Definition{Slug: slug, Actions: []string{"action"}})
	capability := descriptor.Actions[ID(slug)+".action"]
	if calls != 1 || len(capability.ParameterFields) != 2 || capability.ParameterFields[0] != "first" || capability.ParameterFields[1] != "second" || capability.Settings != "inspector.html" {
		t.Fatal(calls, capability)
	}
}
