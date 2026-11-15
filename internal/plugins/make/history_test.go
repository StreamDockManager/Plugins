package makeplugin

import (
	"context"
	"fmt"
	operations "github.com/StreamDockManager/Plugin-SDK/Go/operations"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type receiptTransport func(*http.Request) (*http.Response, error)

// Delegate the fixture HTTP request to its injected response handler
func (f receiptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Create a Make driver with bounded in-memory operation history
func receiptDriver(posts *atomic.Int64) *Driver {
	d := &Driver{HTTP: c.NewHTTP(), BaseURL: "https://fixture.invalid"}
	d.HTTP.Client = &http.Client{Transport: receiptTransport(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		switch {
		case r.Method == "POST":
			posts.Add(1)
			body = `{"executionId":"fixture"}`
		case strings.Contains(r.URL.Path, "/logs/"):
			body = `{"scenarioLog":{"id":"fixture","status":1}}`
		default:
			parts := strings.Split(r.URL.Path, "/")
			body = fmt.Sprintf(`{"scenario":{"id":%s,"name":"Fixture","teamId":1,"isActive":true}}`, parts[len(parts)-1])
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return d
}

// Build Make scenario settings used by receipt tests
func receiptSettings(id int) map[string]any {
	return map[string]any{"_action": "run", "scenario": fmt.Sprint(id), "team_id": "1", "token": "fixture", "inputs": "{}"}
}

// Verify make receipt plateau rejects before POST and keeps unknown outcomes
func TestMakeReceiptPlateauRejectsBeforePOSTAndKeepsUnknownOutcomes(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	ctx := context.Background()
	for i := 1; i <= receiptLimit+100; i++ {
		_, err := d.Command(ctx, "run", receiptSettings(i), 0)
		if (i <= receiptLimit) != (err == nil) {
			t.Fatal("execution admission", i, err)
		}
		s := map[string]any{"_action": "webhook", "webhook": fmt.Sprintf("https://fixture.invalid/hook/%d", i), "inputs": "{}"}
		_, err = d.Command(ctx, "webhook", s, 0)
		if (i <= receiptLimit) != (err == nil) {
			t.Fatal("webhook admission", i, err)
		}
	}
	if posts.Load() != receiptLimit*2 || d.Executions.Len() != receiptLimit || d.Webhooks.Len() != receiptLimit {
		t.Fatal("receipt cap caused untracked delivery", posts.Load())
	}
	d.Demand(nil)
	if d.Executions.Len() != receiptLimit || d.Webhooks.Len() != receiptLimit {
		t.Fatal("unknown outcomes were forgotten")
	}
	for range 3 {
		if _, err := d.Read(ctx, receiptSettings(1)); err != nil {
			t.Fatal(err)
		}
	}
	if posts.Load() != receiptLimit*2 || d.Executions.Len() != receiptLimit-1 || d.Webhooks.Len() != receiptLimit {
		t.Fatal("read replayed a command or terminal receipt did not retire")
	}
	if _, err := d.Command(ctx, "run", receiptSettings(receiptLimit+1), 0); err != nil {
		t.Fatal("retired slot not reusable", err)
	}
	t.Logf("1024 execution +1024 webhook receipts;100 new each rejected beforePOST;afterterminal retirement executions=%d webhooks=%d totalPOSTs=%d", d.Executions.Len(), d.Webhooks.Len(), posts.Load())
}

// Verify make terminal retirement preserves active and uncertain records
func TestMakeTerminalRetirementPreservesActiveAndUncertainRecords(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	ctx := context.Background()
	s := receiptSettings(7)
	d.Demand([]map[string]any{s})
	if _, err := d.Command(ctx, "run", s, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	if !d.execution("7").Terminal {
		t.Fatal("active terminal receipt lost")
	}
	_, _ = d.reserveExecution("8")
	d.storeExecution("8", Execution{State: "Run delivery uncertain · no automatic retry"})
	_, _ = d.reserveExecution("9")
	d.storeExecution("9", Execution{ID: "pending", State: "Running"})
	d.Demand(nil)
	if _, ok := d.Executions.Get("7"); ok {
		t.Fatal("terminal inactive record retained")
	}
	if d.Executions.Len() != 2 || d.execution("8").State == "" || d.execution("9").ID != "pending" {
		t.Fatal("uncertain/pending receipt lost")
	}
	for i := 100; i < 2000; i++ {
		if _, err := d.Command(ctx, "status", receiptSettings(i), 0); err != nil {
			t.Fatal(err)
		}
	}
	if d.Executions.Len() != 2 {
		t.Fatal("read-only status created history")
	}
	if _, err := d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatal("retired receipt replayed")
	}
}

// Verify make demand during submission does not lose receipt
func TestMakeDemandDuringSubmissionDoesNotLoseReceipt(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	upstream := d.HTTP.Client.Transport
	started, release := make(chan struct{}), make(chan struct{})
	d.HTTP.Client.Transport = receiptTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			close(started)
			<-release
		}
		return upstream.RoundTrip(r)
	})
	finished := make(chan error, 1)
	go func() { _, err := d.Command(context.Background(), "run", receiptSettings(7), 0); finished <- err }()
	<-started
	d.Demand(nil)
	if d.execution("7").State == "" {
		t.Fatal("in-flight receipt lost")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if d.execution("7").ID != "fixture" || posts.Load() != 1 {
		t.Fatal("pending receipt lost or replayed")
	}
}

// Verify make discovery and execution strings are bounded
func TestMakeDiscoveryAndExecutionStringsAreBounded(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	oversized := receiptSettings(7)
	oversized["scenario"] = strings.Repeat("0", 129) + "7"
	if _, err := d.Command(context.Background(), "run", oversized, 0); err == nil || posts.Load() != 0 || d.Executions.Len() != 0 {
		t.Fatal("oversized numeric history key admitted", err)
	}
	s := receiptSettings(7)
	if _, err := d.Command(context.Background(), "run", s, 0); err != nil {
		t.Fatal(err)
	}
	upstream := d.HTTP.Client.Transport
	d.HTTP.Client.Transport = receiptTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/logs/") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"scenarioLog":{"id":"fixture","displayStatus":%q}}`, strings.Repeat("x", executionStateBytes+1))))}, nil
		}
		return upstream.RoundTrip(r)
	})
	if _, err := d.Read(context.Background(), s); err == nil || d.execution("7").State != "Accepted · completion pending" {
		t.Fatal("oversized status committed", err)
	}
	for _, tc := range []struct{ count, nameBytes int }{{101, 1}, {100, 3000}} {
		d.HTTP.Client.Transport = receiptTransport(func(*http.Request) (*http.Response, error) {
			var body strings.Builder
			body.WriteString(`{"scenarios":[`)
			for i := range tc.count {
				if i > 0 {
					body.WriteByte(',')
				}
				fmt.Fprintf(&body, `{"id":%d,"name":%q}`, i+1, strings.Repeat("x", tc.nameBytes))
			}
			body.WriteString(`]}`)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body.String()))}, nil
		})
		if _, err := d.Discover(context.Background(), s); err == nil {
			t.Fatal("unbounded discovery accepted", tc)
		}
	}
}

// Populate execution history with the requested test records
func seededExecutions(values map[string]Execution) *operations.Ledger[Execution] {
	d := &Driver{}
	for key, value := range values {
		id, _ := d.reserveExecution(key)
		d.storeExecution(id, value)
	}
	return d.Executions
}
