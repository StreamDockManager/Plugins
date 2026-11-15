package microphonecontrols

import (
	"context"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"path/filepath"
	"testing"
	"time"
)

// Verify factory status completion refresh policy
func TestFactoryStatusCompletionRefreshPolicy(t *testing.T) {
	for _, action := range Definition.Actions {
		for _, tc := range []struct {
			name                 string
			settings             map[string]any
			refresh, unavailable bool
			interval             time.Duration
		}{
			{name: "default", interval: time.Second},
			{name: "configured", settings: map[string]any{"update_interval_ms": float64(7000)}, interval: 7 * time.Second},
			{name: "disabled", settings: map[string]any{"update_interval_ms": float64(0)}},
			{name: "refresh", refresh: true},
			{name: "unavailable", unavailable: true, interval: 2 * time.Second},
			{name: "unavailable disabled", unavailable: true, settings: map[string]any{"update_interval_ms": float64(0)}},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				e := New(filepath.Join(t.TempDir(), "state.json"))
				e.Bindings["key"] = binding{Action: action, Settings: tc.settings}
				e.ProbeKeys["key"] = "status:key"
				feature := Factory(e.Engine)
				var got, sample sampledStatus
				driveDisplays(t, e, []displayStep{{Render: func(ctx context.Context, c *sdk.Client) error {
					err := feature.Apply(ctx, c, jobResult{ID: "status:key", Value: probeStatus{Status: displayStatus{Text: "Ready", Unavailable: tc.unavailable}}}, backgroundJob{Refresh: tc.refresh})
					got, sample = e.Statuses["key"], e.Samples["status:key"]
					return err
				}}})
				want := got.At.Add(tc.interval)
				if tc.settings != nil && tc.settings["update_interval_ms"] == float64(0) && !tc.refresh {
					want = engine.RefreshNext(got.At, 0)
				}
				if got.At.IsZero() || !got.Next.Equal(want) {
					t.Fatalf("wrong schedule: %+v, want %v", got, want)
				}
				if got.Status.Unavailable != tc.unavailable || sample.At != got.At {
					t.Fatal("completion did not preserve sample")
				}
			})
		}
	}
}
