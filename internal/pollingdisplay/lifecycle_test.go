package pollingdisplay

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/polling"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"testing"
	"time"
)

// TestLifecycleAdmissionZeroIntervalIsolationAndLastGood verifies lifecycle admission zero interval isolation and last good
func TestLifecycleAdmissionZeroIntervalIsolationAndLastGood(t *testing.T) {
	now := time.Unix(1000, 0)
	states := map[string]polling.PollState{"one": {LastValue: "Down", Failures: 2}, "other": {Next: now.Add(time.Hour)}}
	jobs := map[string]engine.BackgroundJob{}
	Attach(states, jobs, "missing", time.Second)
	if len(jobs) != 0 {
		t.Fatal("missing job created")
	}
	Attach(states, jobs, "one", 0)
	if states["one"].Next != polling.Never || states["one"].LastValue != "Down" {
		t.Fatal(states)
	}
	if Due(states, jobs, "one", now) {
		t.Fatal("disabled polling due")
	}
	jobs["one"] = engine.BackgroundJob{}
	Attach(states, jobs, "one", time.Second)
	if jobs["one"].Poll.(*Job).Interval != time.Second {
		t.Fatal(jobs)
	}
	DeferRejected(states, jobs, "one", now)
	if states["one"].Next != polling.Never {
		t.Fatal("busy retry changed")
	}
	delete(jobs, "one")
	DeferRejected(states, jobs, "one", now)
	if Due(states, jobs, "one", now) || !Due(states, jobs, "one", now.Add(time.Second)) {
		t.Fatal("retry deadline")
	}
	if !states["other"].Next.Equal(now.Add(time.Hour)) {
		t.Fatal("cross-binding mutation")
	}
	texts := map[string]string{"one": "working"}
	KeepDisplay(states, texts, "one", false, "previous")
	if texts["one"] != "working" {
		t.Fatal(texts)
	}
	KeepDisplay(states, texts, "one", true, "previous")
	if texts["one"] != "previous" {
		t.Fatal(texts)
	}
	state := states["one"]
	state.Stale = true
	states["one"] = state
	KeepDisplay(states, texts, "one", true, "previous")
	if texts["one"] != "Stale\nDown\nRetrying…" {
		t.Fatal(texts)
	}
}

// TestDeadlineKeepsAdmissionAndPendingWrite verifies deadline keeps admission and pending write
func TestDeadlineKeepsAdmissionAndPendingWrite(t *testing.T) {
	e := fixtureengine.New(engine.Definition{}, "", func(*engine.Engine) engine.Feature { return engine.Feature{} })
	defer func() { clear(e.Jobs); e.PendingWrite = time.Time{}; e.Close() }()
	now := time.Unix(1000, 0)
	e.PendingWrite = now.Add(time.Hour)
	states := map[string]polling.PollState{}
	automatic := func(id, action string, s map[string]any) bool { return id == "allowed" }
	if Deadline(e, states, now, automatic) != e.PendingWrite {
		t.Fatal("empty deadline")
	}
	e.Bindings["denied"] = engine.Binding{}
	e.Bindings["allowed"] = engine.Binding{}
	if Deadline(e, states, now, automatic) != now {
		t.Fatal("new admitted binding not due")
	}
	e.Jobs["allowed"] = engine.BackgroundJob{}
	if Deadline(e, states, now, automatic) != e.PendingWrite {
		t.Fatal("busy or denied binding scheduled")
	}
	delete(e.Jobs, "allowed")
	states["allowed"] = polling.PollState{Next: polling.Never}
	if Deadline(e, states, now, automatic) != e.PendingWrite {
		t.Fatal("write delayed by disabled poll")
	}
}
