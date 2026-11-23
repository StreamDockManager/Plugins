package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
	"github.com/StreamDockManager/Plugin-SDK/Go/managedstate"
)

const portableStateKey = "_official_runtime_v1"
const stateNamespace = "official-state-v1"

// Product policy selects a backend; the SDK owns record diffs and recovery
var managedStates sync.Map // *Engine -> *managedState; event-loop owned values
type managedState struct {
	engine                    *Engine
	kind                      string
	target                    any
	client                    *sdk.Client
	state                     *managedstate.State
	images                    *packageImages
	ready, dirty, unavailable bool
}

type managedHandler struct {
	*Engine
}

// Close releases the owned resources and pending work
func (h *managedHandler) Close() error {
	var pending bool
	if value, ok := managedStates.Load(h.Engine); ok {
		state := value.(*managedState)
		pending = state.ready && state.dirty && !state.unavailable && state.kind != "session"
	}
	err := h.Engine.Close()
	if pending {
		// The transport has ended. Report the unresolved write instead of trying
		// another backend or claiming a successful shutdown flush
		err = errors.Join(err, fmt.Errorf("host state has an unconfirmed write at shutdown"))
	}
	return err
}

// stateKind selects the persistence kind for this state definition
func stateKind(slug string) string {
	switch slug {
	case "counter-tally", "timers-pomodoro", "spotify":
		return "portable"
	case "internet-speed-test", "randomizer-dice", "steam-library":
		return "private"
	default:
		return "session"
	}
}

// newManaged constructs managed state with the selected backend
func newManaged(def Definition, factory Factory) *Engine {
	return authoring.New(def, "", func(e *Engine) Feature {
		state := &managedState{engine: e, kind: stateKind(def.Slug)}
		managedStates.Store(e, state)
		feature := PresentedFactory(def, factory)(e)
		before, after, poll, closeFeature := feature.Before, feature.After, feature.Poll, feature.Close
		// Initialize persisted state before exposing an event to the feature handler
		feature.Before = func(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
			if err := state.initialize(ctx, c, def); err != nil {
				return true, err
			}
			if before != nil {
				return before(ctx, c, ev)
			}
			return false, nil
		}
		// Preserve the feature failure while also reporting a dirty-state persistence failure
		feature.After = func(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
			var err error
			if after != nil {
				err = after(ctx, c, ev)
			}
			if state.ready && state.dirty {
				err = errors.Join(err, state.persist(ctx))
			}
			return err
		}
		feature.Poll = func(ctx context.Context, c *sdk.Client) (bool, error) {
			if err := state.initialize(ctx, c, def); err != nil {
				return true, err
			}
			var handled bool
			var err error
			if poll != nil {
				handled, err = poll(ctx, c)
			}
			if state.dirty {
				err = errors.Join(err, state.persist(ctx))
			}
			return handled, err
		}
		feature.Close = func() {
			if closeFeature != nil {
				closeFeature()
			}
			managedStates.Delete(e)
		}
		return withFailureNotifications(def, e, feature)
	})
}

// Restore registers the destination in production and only reads the explicitly
// supplied file path in explicit fixtureengine instances. No legacy file is imported
func Restore(e *Engine, dst any) error {
	if v, ok := managedStates.Load(e); ok {
		v.(*managedState).target = dst
		return nil
	}
	return e.Restore(dst)
}

// MarkDirty marks changed state for a later persistence attempt
func MarkDirty(e *Engine) {
	if v, ok := managedStates.Load(e); ok {
		v.(*managedState).dirty = true
		return
	}
	e.DirtyState = true
}

// RoutinePersist performs the lifecycle-managed persistence check
func RoutinePersist(e *Engine) error {
	if _, ok := managedStates.Load(e); !ok {
		return e.RoutinePersist()
	}
	MarkDirty(e)
	return Persist(e)
}

// Persist requests persistence of the current state
func Persist(e *Engine) error {
	if v, ok := managedStates.Load(e); ok {
		return v.(*managedState).persist(context.Background())
	}
	return e.Persist()
}

// initialize initializes the owner state before use
func (s *managedState) initialize(ctx context.Context, c *sdk.Client, def Definition) error {
	if s.ready {
		return nil
	}
	if s.client == nil && c != nil && !c.Supports("plugin-protocol-v1") {
		s.images = installedImages(def, s.engine.Feature.Visuals)
		if s.images != nil {
			build := s.engine.Feature.View
			if build == nil {
				build = s.engine.BuildDisplayView
			}
			s.engine.Feature.View = func(id string, status DisplayStatus) Display {
				view := build(id, status)
				view.Image = s.images.image(view.Image)
				return view
			}
		}
	}
	s.client = c
	if s.target == nil || s.kind == "session" {
		s.ready = true
		return nil
	}
	var portable managedstate.PortableBackend
	var private managedstate.PrivateBackend
	var err error
	if s.kind == "portable" {
		portable, err = sdk.NewPortableSettings(c, "global", "")
	} else {
		private, err = sdk.NewHostStore(c, stateNamespace)
		if err != nil {
			if _, ok := errors.AsType[*sdk.UnsupportedFeatureError](err); ok {
				s.unavailable = true
				s.ready = true
				return c.LogStream(ctx, sdk.Stderr, "Private history persistence is unavailable on this host; actions remain available and history lasts for this session only")
			}
		}
	}
	if err != nil {
		return err
	}
	state, err := managedstate.New(s.kind, portableStateKey, portable, private)
	if err != nil {
		return err
	}
	snapshot, err := state.Load(ctx)
	if err != nil {
		return err
	}
	if s.engine.Slug == "timers-pomodoro" {
		// Restart must never replay elapsed timer actions
		for key, raw := range snapshot {
			var value map[string]any
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("invalid timer state")
			}
			if value["deadline"] != float64(0) || value["started"] != float64(0) {
				value["deadline"], value["started"], value["color"] = 0, 0, "Paused"
				snapshot[key], _ = json.Marshal(value)
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(encoded, s.target); err != nil {
		return fmt.Errorf("invalid runtime state: %w", err)
	}
	s.state = state
	s.ready = true
	return nil
}

// persist writes retained state through the configured backend
func (s *managedState) persist(ctx context.Context) error {
	if !s.ready {
		return fmt.Errorf("host state is not initialized")
	}
	if s.kind == "session" || s.target == nil || s.unavailable {
		s.dirty = false
		return nil
	}
	s.state.MarkDirty()
	err := s.state.Save(ctx, s.engine.Feature.Snapshot())
	s.dirty = s.state.Dirty
	return err
}
