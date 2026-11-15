package integration

import (
	"context"
	"encoding/json"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// TestEveryPluginDeclaresViewOwnership verifies every plugin declares view ownership
func TestEveryPluginDeclaresViewOwnership(t *testing.T) {
	if len(Definitions) != 24 {
		t.Fatalf("audited %d plugins, want 24", len(Definitions))
	}
	for _, def := range Definitions {
		t.Run(def.Slug, func(t *testing.T) {
			if err := engine.ValidateViewTargets(def); err != nil {
				t.Fatal(err)
			}
			for _, rule := range def.ViewTargets {
				if !slices.Contains(rule.UpdatedBy, rule.Action) {
					t.Fatal("own-action updater missing", rule)
				}
			}
			data, err := json.Marshal(def)
			if err != nil || strings.Contains(string(data), "ViewTargets") || strings.Contains(string(data), "UpdatedBy") {
				t.Fatal("private routing changed the public descriptor", string(data), err)
			}
		})
	}
}

// TestEveryPluginIsolatesCommandFeedbackOnAllSurfaces verifies every plugin isolates command feedback on all surfaces
func TestEveryPluginIsolatesCommandFeedbackOnAllSurfaces(t *testing.T) {
	// Exercise the real per-plugin definitions against a deliberately broadcasting
	// feature. No native action or permission prompt is needed to check the guard
	for _, def := range Definitions {
		for _, target := range def.ViewTargets {
			if target.Live {
				continue // Explicit status/readout views are covered by feature tests
			}
			t.Run(def.Slug+"/"+target.Action, func(t *testing.T) {
				var rendered []string
				e := fixtureengine.New(def, filepath.Join(t.TempDir(), "state.json"), func(e *engine.Engine) engine.Feature {
					return engine.Feature{Render: func(_ context.Context, _ *sdk.Client, id string) error {
						rendered = append(rendered, id)
						return nil
					}, Action: func(ctx context.Context, c *sdk.Client, _ sdk.Event, _ engine.Binding) error {
						for id := range e.Bindings {
							if err := e.Render(ctx, c, id); err != nil {
								return err
							}
						}
						return nil
					}}
				})
				defer e.Close()
				for _, action := range def.Actions {
					for _, id := range []string{"key", "dial", "touch"} {
						e.Bindings[action+"/"+id] = engine.Binding{Action: action, Surface: &sdk.Surface{ControlID: id, Width: 112, Height: 112}}
					}
				}
				for _, action := range def.Actions {
					for index, kind := range []string{"keyDown", "dialDown", "touchTap"} {
						rendered = nil
						id := action + "/" + []string{"key", "dial", "touch"}[index]
						if err := e.Handle(context.Background(), nil, sdk.Event{Event: kind, Context: id, Action: action}); err != nil {
							t.Fatal(err)
						}
						for _, control := range []string{"key", "dial", "touch"} {
							destination := target.Action + "/" + control
							if slices.Contains(rendered, destination) != (id == destination) {
								t.Fatalf("%s changed command %s: %v", id, destination, rendered)
							}
						}
					}
				}
			})
		}
	}
}
