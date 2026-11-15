package pollingdisplay

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/httpclient"
	"github.com/StreamDockManager/Plugin-SDK/Go/polling"
	"time"
)

// CompletePolling shares presentation and last-observation handling, not the
// product's definition of failure: an observed service Down is a valid sample
func CompletePolling(state *polling.PollState, value, text, reason string, failed bool, now time.Time, interval time.Duration, jitter float64, retry time.Time) (string, bool) {
	state.Complete(value, failed, now, interval, jitter, retry)
	last, _ := state.LastValue.(string)
	state.Stale = state.Stale && last != ""
	if !failed {
		return text, false
	}
	if state.Stale {
		return "Stale\n" + last + "\n" + httpclient.RetryLabel(state.Next, now), true
	}
	if reason == "" {
		reason = "Read failed"
	}
	return "Unavailable\n" + reason + "\n" + httpclient.RetryLabel(state.Next, now), false
}

// PollingFeedback builds status feedback for the polling action
func PollingFeedback(state polling.PollState, err error) httpclient.PollingFeedback {
	info := httpclient.PollingFeedback{}
	if state.Failures > 0 {
		info.Stale = state.Stale
		info.NextRetry = state.Next.UTC().Format(time.RFC3339)
		info.LastValue, _ = state.LastValue.(string)
		if err != nil {
			info.Failure = err.Error()
		}
	}
	return info
}
