package catalog

import (
	"encoding/json"
	"path"
	"testing"
)

// Every maintained producer must publish explicit current inspector/layout assets
func TestEveryOfficialPluginPublishesCurrentContracts(t *testing.T) {
	if len(Definitions) != 24 {
		t.Fatal("review the consumer inventory when adding/removing plugins")
	}
	for _, d := range Definitions {
		t.Run(d.Slug, func(t *testing.T) {
			descriptor, assets := Descriptor(d), PluginAssets(d)
			if descriptor.Version != 1 || descriptor.SettingsProtocol != 1 || descriptor.ID != ID(d.Slug) {
				t.Fatal("missing canonical managed descriptor")
			}
			check := func(name string) {
				if path.Ext(name) == "" || len(assets[name]) == 0 {
					t.Fatalf("non-exact or missing asset %q", name)
				}
			}
			check(descriptor.Icon.Light)
			check(descriptor.Icon.Dark)
			fields := append([]Field{}, PluginFields(d.Slug)...)
			for _, action := range d.Actions {
				capability := descriptor.Actions[ID(d.Slug)+"."+action]
				if len(capability.ControlTypes) == 0 {
					t.Fatal("missing explicit controls", action)
				}
				check(capability.Icon.Light)
				check(capability.Icon.Dark)
				check(capability.FeedbackLayout)
				fields = append(fields, ActionFields(d.Slug, action)...)
				var layout struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(assets[capability.FeedbackLayout], &layout); err != nil {
					t.Fatal(err)
				}
				for _, item := range layout.Items {
					if item["key"] == "caption-background" || item["key"] == "backdrop" || item["key"] == "background" {
						meta, ok := item["streamDockManager"].(map[string]any)
						if !ok || meta["role"] != "background" {
							t.Fatal("background lacks explicit role", item)
						}
					}
				}
			}
			for _, field := range fields {
				if field.Type == "path" && field.PathPicker != "file" && field.PathPicker != "folder" && field.PathPicker != "save" {
					t.Fatal("path lacks explicit picker mode", field.Key)
				}
				if (d.Slug == "http-webhooks" || d.Slug == "service-monitor") && field.Key == "interval" {
					t.Fatal("obsolete polling field")
				}
			}
		})
	}
}
