package pollingdisplay

import (
	"errors"
	"github.com/StreamDockManager/Plugin-SDK/Go/polling"
	"strings"
	"testing"
	"time"
)

// TestPollingObservationAndFailureRemainDistinct verifies polling observation and failure remain distinct
func TestPollingObservationAndFailureRemainDistinct(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, sample := range []string{"Available", "Down"} {
		state := polling.PollState{}
		text, retain := CompletePolling(&state, sample, sample, "", false, now, time.Second, 0, time.Time{})
		if retain || text != sample || !state.Available || state.Failures != 0 {
			t.Fatal(text, state)
		}
		text, retain = CompletePolling(&state, "Unknown", "Unknown", "timeout", true, now.Add(time.Second), time.Second, 0, now.Add(time.Minute))
		if !retain || !strings.HasPrefix(text, "Stale\n"+sample) || state.LastValue != sample || !state.ObservedAt.Equal(now) || state.Next.Before(now.Add(time.Minute)) {
			t.Fatal(text, state)
		}
		info := PollingFeedback(state, errors.New("timeout"))
		if !info.Stale || info.LastValue != sample || info.Failure != "timeout" {
			t.Fatal(info)
		}
	}
	state := polling.PollState{}
	text, retain := CompletePolling(&state, "", "", "", true, now, time.Second, 0, time.Time{})
	if retain || state.Stale || !strings.HasPrefix(text, "Unavailable\nRead failed") {
		t.Fatal(text, state)
	}
}
