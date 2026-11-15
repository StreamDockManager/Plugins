package integration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"github.com/StreamDockManager/Plugins/internal/catalog"
)

// Exercise every product identity through its real authoring adapter. The hook
// supplies controlled deferred work; plugin action/driver tests own native/service
// behavior. This test must never invoke an installed app, account, or device
func TestEveryOfficialActionSupportsDeferredCaptureLogging(t *testing.T) {
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-logs-v1","action-results-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	actions := 0
	for _, definition := range Definitions {
		t.Run(definition.Slug, func(t *testing.T) {
			engine := New(definition.Slug, filepath.Join(t.TempDir(), "state.json"))
			defer engine.Close()
			recorder := testkit.New(host)
			engine.Handle(context.Background(), recorder.Client, sdk.Message("didReceiveGlobalSettings", "", map[string]any{"settings": map[string]any{"api_key": "fixture-private-key"}}))
			recorder.Drain()
			descriptor := catalog.Descriptor(definition)
			for _, action := range definition.Actions {
				actions++
				id := catalog.ID(definition.Slug) + "." + action
				event := sdk.Event{Event: "keyDown", Action: id, Context: "fixture"}
				if slices.Contains(descriptor.Actions[id].ControlTypes, "encoder") {
					event = sdk.Message("dialRotate", "fixture", map[string]any{"ticks": 1})
					event.Action = id
				}
				event.Surface = &sdk.Surface{RequestID: id}
				for _, failure := range []bool{false, true} {
					var complete func(error)
					engine.Feature.After = nil
					engine.Feature.Before = func(ctx context.Context, _ *sdk.Client, _ sdk.Event) (bool, error) {
						complete = sdk.DeferAction(ctx)
						return true, nil
					}
					if err := recorder.Replay(context.Background(), engine, []sdk.Event{event}); err != nil {
						t.Fatal(err)
					}
					if values := recorder.Drain(); len(values) != 0 {
						t.Fatal("completed before deferred work", id, values)
					}
					var outcome error
					if failure {
						outcome = errors.New("controlled failure")
					}
					complete(outcome)
					complete(nil)
					logs, receipts := 0, 0
					for _, value := range recorder.Drain() {
						if value.Event == "actionResult" {
							receipts++
						}
						if value.Event == "logMessage" {
							logs++
							var payload struct{ Message, Stream string }
							_ = json.Unmarshal(value.Payload, &payload)
							if !strings.Contains(payload.Message, id) || strings.Contains(payload.Message, "fixture-private-key") {
								t.Fatal(id, payload)
							}
							want := "stdout"
							if failure {
								want = "stderr"
							}
							if payload.Stream != want {
								t.Fatal(id, payload)
							}
						}
					}
					if logs != 1 || receipts != 1 {
						t.Fatal(id, logs, receipts)
					}
				}
			}
		})
	}
	if actions == 0 {
		t.Fatal("empty official action inventory")
	}
	t.Logf("verified capture adapter success/failure for %d actions in %d plugins", actions, len(Definitions))
}
