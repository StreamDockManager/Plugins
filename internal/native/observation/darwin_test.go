//go:build darwin

package observation

import (
	"context"
	"errors"
	"testing"
)

func TestNativeInvalidationAndFallback(t *testing.T) {
	var monitor Monitor
	changed, reliable := true, true
	registrations, calls := 0, 0
	failure := errors.New("fixture failed")
	var readErr error
	open := func() func() (bool, bool) { registrations++; return func() (bool, bool) { return changed, reliable } }
	read := func(context.Context) (string, error) { calls++; return "status", readErr }
	sample := func(want int, err error) {
		t.Helper()
		value, got := monitor.Read(context.Background(), open, read)
		if got != err || (got == nil && value != "status") || calls != want || registrations != 1 {
			t.Fatalf("value %q, error %v, reads %d, registrations %d", value, got, calls, registrations)
		}
	}
	sample(1, nil)
	changed = false
	sample(1, nil)
	changed = true
	readErr = failure
	sample(2, failure)
	changed = false
	readErr = nil
	sample(3, nil)
	if _, err := monitor.Read(Refresh(context.Background()), open, read); err != nil || calls != 4 {
		t.Fatal("manual refresh did not read the platform", err, calls)
	}
	reliable = false
	sample(5, nil)
	sample(6, nil)
}
