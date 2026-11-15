//go:build linux || windows

package spotify

import (
	"context"
	"sync/atomic"
)

// Close releases the owned resources and pending work
func (o *mediaObservation) Close() { o.closed.Store(true); o.healthy.Store(false); o.cancel() }

// Done returns the signal closed when observation stops
func (o *mediaObservation) Done() bool {
	select {
	case <-o.done:
		return true
	default:
		return false
	}
}

// Healthy reports whether native media observation remains healthy
func (o *mediaObservation) Healthy() bool { return o.healthy.Load() }

// Revision returns the current media-change generation
func (o *mediaObservation) Revision() uint64 { return o.revision.Load() }

// invalidate marks the media observation unhealthy and advances its generation
func (o *mediaObservation) invalidate() {
	if o.closed.Load() {
		return
	}
	o.revision.Add(1)
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// ready marks the observer ready to provide change notifications
func (o *mediaObservation) ready(healthy bool) {
	if o.closed.Load() {
		return
	}
	o.healthy.Store(healthy)
	if o.closed.Load() {
		o.healthy.Store(false)
		return
	}
	o.invalidate()
}

// newMediaObservation constructs a lifecycle-owned media observer
func newMediaObservation(wake chan<- struct{}, run func(context.Context, *mediaObservation)) MediaObserver {
	ctx, cancel := context.WithCancel(context.Background())
	o := &mediaObservation{done: make(chan struct{}), cancel: cancel, wake: wake}
	go func() {
		defer close(o.done)
		defer o.ready(false)
		run(ctx, o)
	}()
	return o
}

type mediaObservation struct {
	healthy  atomic.Bool
	closed   atomic.Bool
	revision atomic.Uint64
	done     chan struct{}
	cancel   context.CancelFunc
	wake     chan<- struct{}
}
