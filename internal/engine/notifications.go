package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

type failureNotice struct {
	context                  context.Context
	client                   *sdk.Client
	binding, action, message string
}

// failureReporter owns a bounded asynchronous queue and never retains provider errors or settings
// Callers use it only from the authoring event loop
type failureReporter struct {
	cancels    map[string]context.CancelFunc
	definition Definition
	failures   map[string]string
	sent       map[string]time.Time
	queue      chan failureNotice
	stop       chan struct{}
	once       sync.Once
	closeOnce  sync.Once
	now        func() time.Time
	deliver    func(failureNotice)
}

// Create a per-plugin reporter without starting work until a failure occurs
func newFailureReporter(def Definition) *failureReporter {
	r := &failureReporter{definition: def, failures: map[string]string{}, cancels: map[string]context.CancelFunc{}, sent: map[string]time.Time{}, queue: make(chan failureNotice, 32), stop: make(chan struct{}), now: time.Now}
	r.deliver = func(n failureNotice) {
		if n.client == nil {
			return
		}
		ctx, cancel := context.WithTimeout(n.context, 2*time.Second)
		defer cancel()
		if ctx.Err() != nil {
			return
		}
		// Portable per-binding feedback does not depend on notification permission
		_ = n.client.Alert(ctx, n.binding)
		if n.message != "" {
			_ = n.client.CreateNotification(ctx, n.action, sdk.NotificationError, n.message)
		}
	}
	return r
}

// Release the bounded sender without waiting for a slow host request
func (r *failureReporter) close() {
	r.closeOnce.Do(func() {
		for _, cancel := range r.cancels {
			cancel()
		}
		close(r.stop)
	})
}

// Invalidate queued feedback before recovery or binding removal can reuse its context
func (r *failureReporter) recover(id string) {
	if cancel := r.cancels[id]; cancel != nil {
		cancel()
	}
	delete(r.cancels, id)
	delete(r.failures, id)
}

// Drain reviewed messages outside the plugin event loop and stop promptly at shutdown
func (r *failureReporter) run() {
	for {
		select {
		case <-r.stop:
			return
		case n := <-r.queue:
			select {
			case <-r.stop:
				return
			default:
			}
			r.deliver(n)
		}
	}
}

// Use only exact locally-authored explanations and otherwise provide a generic safe remediation
func safeFailureMessage(def Definition, failure string) string {
	switch failure {
	case "confirm noncommercial free-service use in inspector or configure paid/self-hosted endpoint":
		return "enable noncommercial Open-Meteo use or configure a paid or self-hosted endpoint"
	case "Choose a location in Manager":
		return "choose a location in Manager and review this action's location access"
	case "invalid latitude/longitude":
		return "enter a valid latitude and longitude in the action settings"
	case "choose Celsius or Fahrenheit":
		return "select Celsius or Fahrenheit in the action settings"
	case "weather endpoint must use HTTPS":
		return "configure an HTTPS weather endpoint"
	}
	return "Action unavailable. Check the plugin settings, permissions and service connection"
}

// Exclude progress and stale cached values from failure reporting
func failureStatus(status DisplayStatus) bool {
	text := strings.ToLower(strings.TrimSpace(status.Text))
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "stale" {
			return false
		}
	}
	for _, prefix := range []string{"collecting", "loading", "waiting", "connecting", "idle", "stale"} {
		if strings.HasPrefix(text, prefix) {
			return false
		}
	}
	return status.Unavailable || strings.HasPrefix(text, "unavailable\n") || strings.HasPrefix(text, "error\n") || strings.HasPrefix(text, "failed\n")
}

// Queue one indicator per failure episode and at most one notification per action each minute
func (r *failureReporter) report(c *sdk.Client, id string, binding Binding, failure string) {
	if id == "" || c == nil {
		return
	}
	valid := false
	for _, action := range r.definition.Actions {
		if binding.Action == ID(r.definition.Slug)+"."+action {
			valid = true
			break
		}
	}
	if !valid {
		return
	}
	message := safeFailureMessage(r.definition, failure)
	if r.failures[id] == message {
		return
	}
	if len(r.failures) >= 256 {
		return
	}
	lifetime, cancel := context.WithCancel(context.Background())
	notice := failureNotice{context: lifetime, client: c, binding: id, action: binding.Action, message: message}
	key := binding.Action
	now := r.now()
	if at, ok := r.sent[key]; ok && now.Sub(at) < time.Minute {
		notice.message = ""
	}
	select {
	case <-r.stop:
		cancel()
		return
	default:
	}
	select {
	case r.queue <- notice:
		if old := r.cancels[id]; old != nil {
			old()
		}
		r.cancels[id] = cancel
		r.failures[id] = message
		if notice.message != "" {
			r.sent[key] = now
		}
		r.once.Do(func() { go r.run() })
	default:
		cancel()
		// A later display update may retry when the bounded queue has capacity
	}
}

// Apply official notification policy without changing the behavior of SDK consumers
func withFailureNotifications(def Definition, e *Engine, feature Feature) Feature {
	r := newFailureReporter(def)
	observe, filter, unbound, closeFeature := feature.ObserveFailure, feature.FilterStatus, feature.Unbound, feature.Close
	feature.ObserveFailure = func(ctx context.Context, c *sdk.Client, id string, err error) {
		if observe != nil {
			observe(ctx, c, id, err)
		}
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		if binding, ok := e.Bindings[id]; ok {
			r.report(c, id, binding, err.Error())
		}
		// Unscoped polling errors have no safe action attribution and are not broadcast to unrelated keys
	}
	feature.FilterStatus = func(ctx context.Context, c *sdk.Client, id string, status DisplayStatus) DisplayStatus {
		if filter != nil {
			status = filter(ctx, c, id, status)
		}
		if failureStatus(status) {
			r.report(c, id, e.Bindings[id], status.Text)
			switch status.Text {
			case "Down", "Unknown", "Unavailable", "Failed":
			default:
				status.Text = "Unavailable"
			}
			status.Content = nil
		} else if !status.Unavailable {
			r.recover(id)
		}
		return status
	}
	feature.Unbound = func(id string) {
		r.recover(id)
		if unbound != nil {
			unbound(id)
		}
	}
	feature.Close = func() {
		r.close()
		if closeFeature != nil {
			closeFeature()
		}
	}
	return feature
}
