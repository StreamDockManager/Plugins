package httpwebhooks

import (
	"context"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"path/filepath"
	"testing"
)

// Verify inactive and unsupported events remain handled
func TestInactiveAndUnsupportedEventsRemainHandled(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	actions := append(append([]string(nil), Definition.Actions...), "unsupported")
	for _, action := range actions {
		for _, event := range []string{"keyUp", "dialUp", "unsupportedEvent"} {
			ev := sdk.Event{Event: event, Action: action, Context: "fixture"}
			handled, err := p.extensionEvent(context.Background(), nil, ev, binding{Action: action})
			if !handled || err != nil {
				t.Fatalf("%s/%s: handled=%v err=%v", action, event, handled, err)
			}
			if err := p.handleAction(context.Background(), nil, ev, binding{Action: action}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(p.Jobs) != 0 || p.DirtyState {
		t.Fatal("inactive input performed work")
	}
}
