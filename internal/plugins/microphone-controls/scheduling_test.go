package microphonecontrols

import (
	"path/filepath"
	"testing"
	"time"
)

// Verify command only bindings do not schedule status polling
func TestCommandOnlyBindingsDoNotScheduleStatusPolling(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.Bindings["mute"] = binding{Action: "mute"}
	e.Bindings["unmute"] = binding{Action: "unmute"}
	e.ActivateView("mute")
	if next := e.NextPoll(time.Now()); !next.IsZero() {
		t.Fatal("command feedback created idle status polling", next)
	}
}
