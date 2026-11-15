package engine

import (
	"sync/atomic"
	"time"
)

// Snapshot coordinates revision/freshness bookkeeping under the driver's lock
// Drivers retain session validation, transport, readiness, and domain limits
type Snapshot[T any] struct {
	Value    *T
	Revision uint64
	At       time.Time
}

// Fresh requires a retained value, matching revision and an unexpired freshness window
func (s *Snapshot[T]) Fresh(revision uint64, now time.Time, interval time.Duration) bool {
	return s.Value != nil && s.Revision == revision && now.Sub(s.At) < interval
}

// Store records the current observation snapshot
func (s *Snapshot[T]) Store(value *T, revision uint64, now time.Time) {
	s.Value, s.Revision, s.At = value, revision, now
}

// SignalObservation notifies consumers that an observation changed
func SignalObservation(dirty *atomic.Bool, wake chan<- struct{}) {
	dirty.Store(true)
	select {
	case wake <- struct{}{}:
	default:
	}
}
