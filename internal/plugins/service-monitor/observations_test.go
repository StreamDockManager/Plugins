package servicemonitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verify observed HTTP down replaces success and unknown keeps last observation
func TestObservedHTTPDownReplacesSuccessAndUnknownKeepsLastObservation(t *testing.T) {
	code := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(code)
	}))
	defer server.Close()
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	now := time.Now()
	job := pollingJob{Interval: 30 * time.Second}
	for _, status := range []int{200, 503, 404} {
		code = status
		text, value, err := checkService(context.Background(), "http", map[string]any{"url": server.URL}, newHTTPValidatorCache(), false)
		if err != nil {
			t.Fatal(err)
		}
		got, retained := p.completePolling("key", job, jobResult{Text: text, Value: value}, now, 0)
		if retained || strings.Contains(got, "Stale") {
			t.Fatal(got)
		}
		if status != 200 && (!strings.Contains(got, "Down") || !strings.Contains(got, http.StatusText(status)) && !strings.Contains(got, "HTTP ")) {
			t.Fatal(got)
		}
		if status != 200 && p.polling["key"].Next.Before(now.Add(100*time.Second)) {
			t.Fatal("Retry-After lost")
		}
	}
	got, retained := p.completePolling("key", job, jobResult{Value: serviceResult{Unknown: true, Reason: "timeout"}, Err: errors.New("timeout")}, now.Add(time.Second), 0)
	state := p.polling["key"]
	if !state.Available || !state.ObservedAt.Equal(now) || !strings.HasPrefix(state.LastValue.(string), "Down") {
		t.Fatal("unknown result replaced the last observed status", state)
	}
	feedback := p.annotatePolling("key", jobResult{Value: serviceResult{Unknown: true}, Err: errors.New("timeout")}).(serviceResult)
	if !feedback.Stale || feedback.LastValue != state.LastValue || feedback.Failure != "timeout" {
		t.Fatal("unknown result lost last observation in feedback", feedback)
	}
	if !retained || !strings.Contains(got, "Stale\nDown") || strings.Contains(got, "Available") {
		t.Fatal(got)
	}
}

// Verify known stopped service versus unrecognized or permission failure
func TestKnownStoppedServiceVersusUnrecognizedOrPermissionFailure(t *testing.T) {
	for _, tc := range []struct {
		platform, text string
		err            error
		known          bool
	}{
		{"linux", "inactive", errors.New("exit status 3"), true},
		{"linux", "permission denied", errors.New("exit status 1"), false},
		{"windows", "STATE : 1 STOPPED", nil, true},
		{"windows", "unrecognized output", nil, false},
		{"darwin", "PID Status Label\n- 0 fixture", nil, true},
	} {
		available, _, err := localServiceObservation(tc.platform, "fixture", tc.text, tc.err)
		if available || (err == nil) != tc.known {
			t.Fatal(tc, available, err)
		}
	}
}

// Verify empty polling display remains unavailable on failure
func TestEmptyPollingDisplayRemainsUnavailableOnFailure(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	now := time.Now()
	job := pollingJob{Interval: time.Second}
	p.completePolling("empty", job, jobResult{Value: serviceResult{Available: true}}, now, 0)
	text, retain := p.completePolling("empty", job, jobResult{Err: errors.New("offline")}, now.Add(time.Second), 0)
	if retain || !strings.HasPrefix(text, "Unavailable\n") || p.polling["empty"].Stale {
		t.Fatal("empty display became a stale fallback", text, p.polling["empty"])
	}
}

// Verify binding settings reset retains peer observation
func TestBindingSettingsResetRetainsPeerObservation(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	now := time.Now()
	p.completePolling("peer", pollingJob{Interval: time.Minute}, jobResult{Text: "Down\nchecked", Value: serviceResult{}}, now, 0)
	p.completePolling("edited", pollingJob{Interval: time.Minute}, jobResult{Text: "Available", Value: serviceResult{Available: true}}, now, 0)
	peer := p.polling["peer"]
	p.ResultsText["peer"] = "Down\nchecked"
	p.settingsFeature("edited")
	if p.polling["peer"] != peer || p.ResultsText["peer"] != "Down\nchecked" {
		t.Fatal("binding settings cleared peer observation")
	}
	if _, ok := p.polling["edited"]; ok {
		t.Fatal("edited binding retained old observation")
	}
	p.settingsFeature("")
	if len(p.polling) != 0 || len(p.ResultsText) != 0 {
		t.Fatal("global settings did not clear all observations")
	}
}
