package pollingdisplay

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/polling"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"time"
)

type Job struct{ Interval time.Duration }

// Attach attaches polling state to the live action context
func Attach(states map[string]polling.PollState, jobs map[string]engine.BackgroundJob, id string, interval time.Duration) {
	if interval == 0 {
		state := states[id]
		state.Next = polling.Never
		states[id] = state
		return
	}
	job, ok := jobs[id]
	if !ok {
		return
	}
	job.Poll = &Job{Interval: interval}
	jobs[id] = job
}

// DeferRejected schedules a short retry when no polling job was admitted
func DeferRejected(states map[string]polling.PollState, jobs map[string]engine.BackgroundJob, id string, now time.Time) {
	if _, busy := jobs[id]; busy {
		return
	}
	state := states[id]
	state.Next = now.Add(time.Second)
	states[id] = state
}

// Due reports whether this action is ready for another poll
func Due(states map[string]polling.PollState, jobs map[string]engine.BackgroundJob, id string, now time.Time) bool {
	if _, busy := jobs[id]; busy {
		return false
	}
	return !now.Before(states[id].Next)
}

// KeepDisplay retains the last useful display while a refresh is pending
func KeepDisplay(states map[string]polling.PollState, texts map[string]string, id string, automatic bool, previous string) {
	if !automatic {
		return
	}
	state := states[id]
	last, _ := state.LastValue.(string)
	if state.Stale {
		texts[id] = "Stale\n" + last + "\nRetrying…"
	} else if last != "" {
		texts[id] = previous
	}
}

// Deadline shares scheduling only; each plugin owns automatic-read admission
func Deadline(e *engine.Engine, states map[string]polling.PollState, now time.Time, automatic func(string, string, map[string]any) bool) time.Time {
	if len(e.Bindings) == 0 {
		return e.PendingWrite
	}
	if e.NextPeriodic.IsZero() {
		e.NextPeriodic = now.Add(time.Second)
	}
	var deadline time.Time
	// Retain the earliest nonzero deadline across polling and pending writes
	earlier := func(at time.Time) {
		if !at.IsZero() && (deadline.IsZero() || at.Before(deadline)) {
			deadline = at
		}
	}
	for id, b := range e.Bindings {
		settings := e.EffectiveSettings(id, b.Settings)
		if _, busy := e.Jobs[id]; !busy && automatic(id, engine.Action(b.Action), settings) {
			at := states[id].Next
			if at.IsZero() {
				at = now
			}
			earlier(at)
		}
	}
	earlier(e.PendingWrite)
	return deadline
}
