package servicemonitor

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestLaunchdBatchSharesListingAcrossWorkerWaves(t *testing.T) {
	batch := new(listing)
	var calls atomic.Int32
	run := func(context.Context, string, ...string) (string, error) {
		calls.Add(1)
		return "PID Status Label\n123 0 active\n- 1 idle", nil
	}
	for _, test := range []struct {
		name    string
		running bool
		reason  string
	}{
		{"active", true, ""}, {"idle", false, "stopped"}, {"absent", false, "not loaded"},
		{"active", true, ""}, {"idle", false, "stopped"}, {"absent", false, "not loaded"},
	} {
		index, err := batch.read(context.Background(), run)
		active, reason, err := index.status(test.name, err)
		if err != nil || active != test.running || reason != test.reason {
			t.Fatal(test.name, active, reason, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d listings for six checks", calls.Load())
	}
	_, _ = new(listing).read(context.Background(), run)
	if calls.Load() != 2 {
		t.Fatal("new batch reused stale results")
	}
}
