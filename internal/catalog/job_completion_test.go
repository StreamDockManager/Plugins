package catalog

import (
	"context"
	"errors"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"path/filepath"
	"testing"

	"github.com/StreamDockManager/Plugins/internal/engine"
	batterypower "github.com/StreamDockManager/Plugins/internal/plugins/battery-power"
	microphonecontrols "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
	networkvpn "github.com/StreamDockManager/Plugins/internal/plugins/network-vpn"
	systemmonitor "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
	"github.com/StreamDockManager/Plugins/internal/plugins/weather"
)

// TestPluginJobCompletionPreservesStatusRouting verifies plugin job completion preserves status routing
func TestPluginJobCompletionPreservesStatusRouting(t *testing.T) {
	for _, plugin := range []struct {
		definition engine.Definition
		factory    engine.Factory
	}{
		{batterypower.Definition, batterypower.Factory},
		{microphonecontrols.Definition, microphonecontrols.Factory},
		{networkvpn.Definition, networkvpn.Factory},
		{systemmonitor.Definition, systemmonitor.Factory},
		{weather.Definition, weather.Factory},
	} {
		t.Run(plugin.definition.Slug, func(t *testing.T) {
			e := fixtureengine.New(plugin.definition, filepath.Join(t.TempDir(), "state.json"), plugin.factory)
			defer e.Close()
			for _, result := range []engine.JobResult{
				{ID: "command", Text: "Done"},
				{ID: "command", Err: errors.New("offline")},
			} {
				e.Results[result.ID] = "old result"
				if err := e.Feature.Apply(context.Background(), nil, result, engine.BackgroundJob{}); err != nil {
					t.Fatal(err)
				}
				want := result.Text
				if result.Err != nil {
					want = "Unavailable\noffline"
				}
				if e.ResultsText[result.ID] != want || e.Results[result.ID] != nil {
					t.Fatal("generic completion did not replace prior result", e.ResultsText, e.Results)
				}
			}
			// Status jobs have their own typed sample application. An invalid sample
			// must not fall through to generic result storage or inspector delivery
			e.ResultsText["status:probe"] = "retained"
			e.Results["status:probe"] = "retained"
			if err := e.Feature.Apply(context.Background(), nil, engine.JobResult{ID: "status:probe", Text: "ignored", Value: "invalid sample"}, engine.BackgroundJob{Refresh: true}); err != nil {
				t.Fatal(err)
			}
			if e.ResultsText["status:probe"] != "retained" || e.Results["status:probe"] != "retained" {
				t.Fatal("status completion entered generic path", e.ResultsText, e.Results)
			}
		})
	}
}
