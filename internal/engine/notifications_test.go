package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// Keep progress and cached stale data out of the error notification path
func TestNotificationFailureClassification(t *testing.T) {
	for _, text := range []string{"Collecting…", "Loading", "Waiting for shared location", "Connecting…", "Idle", "Stale\n12 C", "CPU\nStale"} {
		if failureStatus(DisplayStatus{Text: text, Unavailable: true}) {
			t.Fatal(text)
		}
	}
	if !failureStatus(DisplayStatus{Text: "https://secret.example/token", Unavailable: true}) {
		t.Fatal("missing fault")
	}
	if failureStatus(DisplayStatus{Text: "Working"}) {
		t.Fatal("healthy fault")
	}
	def := Definition{Slug: "weather", Name: "Weather"}
	for _, secret := range []string{"https://secret.example/token", "location 51.5,-0.12", "authorization: secret"} {
		if strings.Contains(safeFailureMessage(def, secret), secret) {
			t.Fatal("private provider error forwarded")
		}
	}
	if !strings.Contains(safeFailureMessage(def, "invalid latitude/longitude"), "valid latitude") {
		t.Fatal("safe remediation lost")
	}
}

// Repeated failures do not spam while another binding keeps independent portable feedback
func TestNotificationDebounceAndRecovery(t *testing.T) {
	def := Definition{Slug: "weather", Name: "Weather", Actions: []string{"conditions"}}
	r := newFailureReporter(def)
	defer r.close()
	r.once.Do(func() {}) // Inspect the queue synchronously without a worker or host traffic
	now := time.Now()
	r.now = func() time.Time { return now }
	c, _ := sdk.NewLocalClient(sdk.HostInfo{}, "test")
	b := Binding{Action: ID("weather") + ".conditions"}
	r.report(c, "one", b, "private error")
	n := <-r.queue
	if n.binding != "one" || n.message == "" {
		t.Fatal(n)
	}
	r.report(c, "one", b, "different private error")
	if len(r.queue) != 0 {
		t.Fatal("repeated polling failure")
	}
	r.report(c, "two", b, "private error")
	n = <-r.queue
	if n.binding != "two" || n.message != "" {
		t.Fatal("duplicate toast or missing binding indicator", n)
	}
	r.recover("one")
	now = now.Add(time.Minute)
	r.report(c, "one", b, "private error")
	if (<-r.queue).message == "" {
		t.Fatal("new failure after recovery was suppressed")
	}
}

// Filter error paragraphs but do not report cancellation or waiting states
func TestOfficialFailurePolicyFiltersDockText(t *testing.T) {
	def := Definition{Slug: "weather", Name: "Weather", Actions: []string{"conditions"}}
	e := &Engine{Bindings: map[string]Binding{"one": {Action: ID("weather") + ".conditions"}}}
	f := withFailureNotifications(def, e, Feature{})
	defer f.Close()
	s := f.FilterStatus(context.Background(), nil, "one", DisplayStatus{Text: "https://private.example token=secret", Unavailable: true, Content: &Presentation{Detail: "secret"}})
	if s.Text != "Unavailable" || s.Content != nil {
		t.Fatal(s)
	}
	wait := DisplayStatus{Text: "Waiting for shared location", Unavailable: true}
	if got := f.FilterStatus(context.Background(), nil, "one", wait); got.Text != wait.Text {
		t.Fatal(got)
	}
	f.ObserveFailure(context.Background(), nil, "one", context.Canceled)
	f.ObserveFailure(context.Background(), nil, "one", errors.New("secret"))
}

// Unsupported hosts retain the portable alert and never emit notification requests
func TestFailureFeedbackOnUnsupportedHost(t *testing.T) {
	r := newFailureReporter(Definition{})
	c, out := sdk.NewLocalClient(sdk.HostInfo{}, "test")
	r.deliver(failureNotice{context: context.Background(), client: c, binding: "key", action: "action", message: "Safe failure"})
	select {
	case ev := <-out:
		if ev.Event != "showAlert" || ev.Context != "key" {
			t.Fatal(ev)
		}
	default:
		t.Fatal("missing portable alert")
	}
	select {
	case ev := <-out:
		t.Fatal("unsupported host request", ev)
	default:
	}
}

// Recovery invalidates queued feedback and prevents an old alert on a healthy binding
func TestRecoveryCancelsQueuedFeedback(t *testing.T) {
	def := Definition{Slug: "weather", Actions: []string{"conditions"}}
	r := newFailureReporter(def)
	defer r.close()
	r.once.Do(func() {})
	c, out := sdk.NewLocalClient(sdk.HostInfo{}, "test")
	r.report(c, "one", Binding{Action: ID("weather") + ".conditions"}, "failure")
	n := <-r.queue
	r.recover("one")
	if n.context.Err() == nil {
		t.Fatal("pending notice not cancelled")
	}
	r.deliver(n)
	select {
	case ev := <-out:
		t.Fatal("stale alert", ev)
	default:
	}
}

// A denied optional permission does not suppress the portable indicator or retry indefinitely
func TestFailureFeedbackDeniedPermission(t *testing.T) {
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-permissions-v1","plugin-notifications-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	c, out := sdk.NewLocalClient(host, "fixture")
	r := newFailureReporter(Definition{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.deliver(failureNotice{context: context.Background(), client: c, binding: "key", action: "action", message: "Safe failure"})
	}()
	if ev := <-out; ev.Event != "showAlert" {
		t.Fatal(ev)
	}
	req := <-out
	if req.Method != "create-notification" {
		t.Fatal(req)
	}
	c.ObserveControl(sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID, Error: &sdk.RPCError{Code: "permission_denied", Message: "private host data"}})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("denied notification blocked sender")
	}
	select {
	case ev := <-out:
		t.Fatal("denial retry", ev)
	default:
	}
}

// Aging a failed sample must not disguise its provider error as harmless stale content
func TestStaleFailureRemainsSanitized(t *testing.T) {
	failed := StaleSample(DisplayStatus{Text: "private provider payload", Unavailable: true})
	if !failureStatus(failed) || strings.HasPrefix(failed.Text, "Stale") {
		t.Fatal(failed)
	}
	good := StaleSample(DisplayStatus{Text: "12 C"})
	if failureStatus(good) || good.Text != "Stale\n12 C" {
		t.Fatal(good)
	}
}
