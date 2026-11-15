package engine

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSavedLookupAndCreationKeepReadAndDirtyBoundaries verifies saved lookup and creation keep read and dirty boundaries
func TestSavedLookupAndCreationKeepReadAndDirtyBoundaries(t *testing.T) {
	type value struct{ Count int }
	states := map[string]*value{}
	empty := value{}
	settings := map[string]any{"shared_id": "shared"}
	if LookupSaved(states, "one", settings, &empty) != &empty || len(states) != 0 {
		t.Fatal("read created state")
	}
	saved := EnsureSaved(states, SavedKey("one", settings))
	saved.Count = 9
	if LookupSaved(states, "two", settings, &empty) != saved || len(states) != 1 {
		t.Fatal("shared identity lost")
	}
	if EnsureSaved(states, "shared") != saved || SavedKey("fallback", nil) != "fallback" {
		t.Fatal("lookup changed")
	}
}

// TestCollectSampleCacheClockAdmissionOwnershipAndStalePolicy verifies collect sample cache clock admission ownership and stale policy
func TestCollectSampleCacheClockAdmissionOwnershipAndStalePolicy(t *testing.T) {
	e := New(Definition{}, "", func(*Engine) Feature { return Feature{} })
	defer func() { clear(e.Jobs); e.PendingWrite = time.Time{}; e.Close() }()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	e.Samples["probe"] = SampledStatus{Status: DisplayStatus{Text: "cached"}, At: now, Next: now.Add(time.Second)}
	calls := 0
	start := func() bool { calls++; e.Jobs["probe"] = BackgroundJob{}; return true }
	if got := CollectSample(e, "one", "probe", SampledStatus{}, clock, start, StaleSample); got.Text != "cached" || calls != 0 {
		t.Fatal(got, calls)
	}
	now = now.Add(time.Second)
	CollectSample(e, "one", "probe", e.Statuses["one"], clock, start, StaleSample)
	if calls != 1 || e.Jobs["probe"].Owner != "one" {
		t.Fatal("job ownership", calls, e.Jobs)
	}
	CollectSample(e, "one", "probe", e.Statuses["one"], clock, start, StaleSample)
	if calls != 1 {
		t.Fatal("duplicate busy job")
	}
	delete(e.Jobs, "probe")
	CollectSample(e, "one", "probe", e.Statuses["one"], clock, func() bool { return false }, StaleSample)
	if !e.Statuses["one"].Next.Equal(now.Add(time.Second)) {
		t.Fatal("admission retry clock")
	}
	now = now.Add(10 * time.Second)
	e.Jobs["probe"] = BackgroundJob{}
	got := CollectSample(e, "one", "probe", e.Statuses["one"], clock, func() bool { return false }, func(s DisplayStatus) DisplayStatus { return DisplayStatus{Text: s.Text, Unavailable: true} })
	if got.Text != "cached" || !got.Unavailable {
		t.Fatal("specialized stale policy", got)
	}
	if got := CollectSample(e, "empty", "missing", SampledStatus{}, clock, func() bool { return false }, StaleSample); got.Text != "Collecting…" || !got.Unavailable {
		t.Fatal(got)
	}
}

// TestDiagnosticDeliveryBoundsAndFailures verifies diagnostic delivery bounds and failures
func TestDiagnosticDeliveryBoundsAndFailures(t *testing.T) {
	ctx := context.Background()
	recorder := observationRecorder(t)
	if err := SendDiagnostic(ctx, nil, "", make(chan int)); err != nil {
		t.Fatal(err)
	}
	if err := SendDiagnostic(ctx, nil, "key", nil); err != nil {
		t.Fatal(err)
	}
	if err := SendDiagnostic(ctx, recorder.Client, "key", make(chan int)); err != nil {
		t.Fatal(err)
	}
	failedEncoding := recorder.Drain()
	if len(failedEncoding) != 1 || len(failedEncoding[0].Payload) != 0 {
		t.Fatal("SDK empty-payload compatibility changed", failedEncoding)
	}
	for _, value := range []any{map[string]any{"value": "ok"}, strings.Repeat("x", DiagnosticLimit)} {
		if err := SendDiagnostic(ctx, recorder.Client, "key", value); err != nil {
			t.Fatal(err)
		}
	}
	events := recorder.Drain()
	if len(events) != 2 {
		t.Fatal(events)
	}
	if events[0].Context != "key" || events[0].Event != "sendToPropertyInspector" {
		t.Fatal(events[0])
	}
	var payload map[string]any
	if err := json.Unmarshal(events[1].Payload, &payload); err != nil || payload["truncated"] != true {
		t.Fatal(payload, err)
	}
}

// TestTextAdaptersPreserveErrorsDefaultsAndCustomStateBoundary verifies text adapters preserve errors defaults and custom state boundary
func TestTextAdaptersPreserveErrorsDefaultsAndCustomStateBoundary(t *testing.T) {
	if got := TextStatus("slug", "some-action", "slug", nil); got.Text != "Some-action" || got.State != 0 {
		t.Fatal(got)
	}
	if got := TextStatus("slug", "", "slug", nil); got.Text != "slug" {
		t.Fatal(got)
	}
	if got := TextStatus("slug", "x", "text", errors.New("bad")); got.Text != "Unavailable\nbad" || !got.Unavailable {
		t.Fatal(got)
	}
	if got := AsyncStatus(""); got.Text != "Ready" {
		t.Fatal(got)
	}
	if AsyncStatus("Down").Unavailable || AsyncStatus("Unknown").Unavailable {
		t.Fatal("service classification leaked into ordinary adapter")
	}
	if !AsyncStatus("Stale\nvalue").Unavailable {
		t.Fatal("stale status lost")
	}
}

// TestSnapshotFreshnessRevisionAndWakeCoalescing verifies snapshot freshness revision and wake coalescing
func TestSnapshotFreshnessRevisionAndWakeCoalescing(t *testing.T) {
	var s Snapshot[int]
	now := time.Unix(100, 0)
	if s.Fresh(0, now, time.Second) {
		t.Fatal("empty snapshot fresh")
	}
	v := 5
	s.Store(&v, 4, now)
	if !s.Fresh(4, now, time.Second) || s.Fresh(5, now, time.Second) || s.Fresh(4, now.Add(time.Second), time.Second) {
		t.Fatal("revision or deadline ignored")
	}
	var dirty atomic.Bool
	wake := make(chan struct{}, 1)
	SignalObservation(&dirty, wake)
	SignalObservation(&dirty, wake)
	if !dirty.Swap(false) || len(wake) != 1 {
		t.Fatal("wake not coalesced")
	}
	SignalObservation(&dirty, nil)
}

// This contract checks every action/event branch, including unsupported inputs:
// the removed fallback cannot become reachable without failing this test
func TestExtensionHandlersAlwaysClaimTheirEvents(t *testing.T) {
	for _, slug := range []string{"http-webhooks", "service-monitor", "steam-library", "internet-speed-test"} {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../plugins", slug, "plugin.go"), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "extensionEvent" {
				continue
			}
			found = true
			returns := 0
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if ret, ok := n.(*ast.ReturnStmt); ok {
					returns++
					if len(ret.Results) != 2 {
						t.Fatal(slug, "unhandled return")
					}
					id, ok := ret.Results[0].(*ast.Ident)
					if !ok || id.Name != "true" {
						t.Fatal(slug, "unhandled branch")
					}
				}
				return true
			})
			if returns == 0 {
				t.Fatal(slug, "no action returns")
			}
		}
		if !found {
			t.Fatal(slug, "missing handler")
		}
	}
}

type changingDiagnostic struct{ calls int }

// MarshalJSON encodes the controlled JSON fixture
func (d *changingDiagnostic) MarshalJSON() ([]byte, error) {
	d.calls++
	if d.calls > 1 {
		return json.Marshal(strings.Repeat("x", DiagnosticLimit+1))
	}
	return []byte(`{"value":"first"}`), nil
}

// TestDiagnosticChecksTheDeliveredBytesOnce verifies diagnostic checks the delivered bytes once
func TestDiagnosticChecksTheDeliveredBytesOnce(t *testing.T) {
	recorder := observationRecorder(t)
	value := &changingDiagnostic{}
	if err := SendDiagnostic(context.Background(), recorder.Client, "key", value); err != nil {
		t.Fatal(err)
	}
	events := recorder.Drain()
	if value.calls != 1 || len(events) != 1 || string(events[0].Payload) != `{"value":"first"}` {
		t.Fatal(value.calls, events)
	}
	// A JSON string adds exactly two quote bytes; the inclusive limit remains valid
	if err := SendDiagnostic(context.Background(), recorder.Client, "key", strings.Repeat("x", DiagnosticLimit-2)); err != nil {
		t.Fatal(err)
	}
	if events = recorder.Drain(); len(events) != 1 || len(events[0].Payload) != DiagnosticLimit {
		t.Fatal("inclusive limit changed")
	}
	// Transport admission failures still reach the caller, which owns recovery
	failed := false
	for range 1024 {
		if err := SendDiagnostic(context.Background(), recorder.Client, "key", "small"); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("delivery failure hidden")
	}
}
