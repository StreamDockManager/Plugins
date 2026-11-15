package engine

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestCompleteSampleFiltersAndPreservesPolicyOrder verifies complete sample filters and preserves policy order
func TestCompleteSampleFiltersAndPreservesPolicyOrder(t *testing.T) {
	e := &Engine{ProbeKeys: map[string]string{"a": "probe", "hidden": "probe", "other": "different"}, Bindings: map[string]Binding{"a": {}, "other": {}}, Statuses: map[string]SampledStatus{}, Samples: map[string]SampledStatus{}, ResultsText: map[string]string{"probe": "done"}, Checked: map[string]time.Time{}}
	var trace []string
	now := time.Unix(10, 0)
	err := CompleteSample(e, "probe", func(id string, b Binding, old SampledStatus) SampledStatus {
		trace = append(trace, "policy:"+id)
		return SampledStatus{At: now, Next: now.Add(time.Second), Status: DisplayStatus{Text: "sample"}}
	}, func(id string, status DisplayStatus) error {
		trace = append(trace, "send:"+id)
		if e.Statuses[id].At != now || e.Samples["probe"].At != now {
			t.Fatal("publication preceded bookkeeping")
		}
		return nil
	}, func() {
		trace = append(trace, "after")
		if e.ResultsText["probe"] != "done" {
			t.Fatal("cleanup preceded effects")
		}
	})
	if err != nil || !reflect.DeepEqual(trace, []string{"policy:a", "send:a", "after"}) {
		t.Fatalf("%v %v", trace, err)
	}
	if _, ok := e.ResultsText["probe"]; ok {
		t.Fatal("completion not cleaned")
	}
}

// TestCompleteSampleFailureKeepsEffectsAndCleanupPending verifies complete sample failure keeps effects and cleanup pending
func TestCompleteSampleFailureKeepsEffectsAndCleanupPending(t *testing.T) {
	e := &Engine{ProbeKeys: map[string]string{"a": "p"}, Bindings: map[string]Binding{"a": {}}, Statuses: map[string]SampledStatus{}, Samples: map[string]SampledStatus{}, ResultsText: map[string]string{"p": "keep"}}
	failure := errors.New("display failed")
	err := CompleteSample(e, "p", func(string, Binding, SampledStatus) SampledStatus { return SampledStatus{} }, func(string, DisplayStatus) error { return failure }, func() { t.Fatal("effects ran after failure") })
	if !errors.Is(err, failure) || e.ResultsText["p"] != "keep" {
		t.Fatal("failure ordering changed")
	}
	calls := 0
	e.ProbeKeys = nil
	if err := CompleteSample(e, "p", func(string, Binding, SampledStatus) SampledStatus { t.Fatal("hidden policy"); return SampledStatus{} }, func(string, DisplayStatus) error { t.Fatal("hidden send"); return nil }, func() { calls++ }); err != nil || calls != 1 {
		t.Fatal("no-subscriber completion changed")
	}
}

// TestCompleteSampleKeepsPerBindingSchedules verifies complete sample keeps per binding schedules
func TestCompleteSampleKeepsPerBindingSchedules(t *testing.T) {
	now := time.Unix(100, 0)
	e := &Engine{ProbeKeys: map[string]string{"manual": "p", "automatic": "p"}, Bindings: map[string]Binding{"manual": {}, "automatic": {}}, Statuses: map[string]SampledStatus{"manual": {Failures: 2}, "automatic": {Failures: 4}}, Samples: map[string]SampledStatus{}}
	sent := map[string]int{}
	err := CompleteSample(e, "p", func(id string, _ Binding, previous SampledStatus) SampledStatus {
		interval := time.Second
		if id == "manual" {
			interval = 0
		}
		return SampledStatus{At: now, Next: RefreshNext(now, interval), Failures: previous.Failures, Status: DisplayStatus{Text: id}}
	}, func(id string, s DisplayStatus) error {
		sent[id]++
		if s.Text != id {
			t.Fatal("crossed result")
		}
		return nil
	}, nil)
	if err != nil || len(sent) != 2 || sent["manual"] != 1 || sent["automatic"] != 1 {
		t.Fatal(sent, err)
	}
	if e.Statuses["manual"].Next != RefreshNext(now, 0) || e.Statuses["automatic"].Next != RefreshNext(now, time.Second) || e.Statuses["manual"].Failures != 2 || e.Statuses["automatic"].Failures != 4 {
		t.Fatal("per-binding schedule lost")
	}
}
