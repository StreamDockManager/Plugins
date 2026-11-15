package makeplugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Verify overlapping scenario runs keep separate receipts and poll every known execution
func TestOverlappingScenarioRunsKeepSeparateReceiptsAndPollEveryKnownExecution(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	upstream := d.HTTP.Client.Transport
	started := make(chan int64, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	polled := map[string]int{}
	d.HTTP.Client.Transport = receiptTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			n := posts.Add(1)
			started <- n
			<-release
			if n == 2 {
				return nil, fmt.Errorf("connection lost after submission")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"executionId":"run-%d"}`, n)))}, nil
		}
		if strings.Contains(r.URL.Path, "/logs/") {
			mu.Lock()
			polled[r.URL.Path]++
			mu.Unlock()
		}
		return upstream.RoundTrip(r)
	})
	settings := receiptSettings(7)
	d.Demand([]map[string]any{settings})
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := d.Command(context.Background(), "run", settings, 0); done <- err }()
	}
	<-started
	<-started
	if d.pendingCount("7") != 2 || d.Executions.Len() != 2 {
		t.Fatal("second submission overwrote first")
	}
	close(release)
	err1, err2 := <-done, <-done
	if (err1 == nil) == (err2 == nil) {
		t.Fatal(err1, err2)
	}
	value, err := d.Read(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if value.(Sample).Pending != 1 || len(polled) != 1 || d.Executions.Len() != 2 {
		t.Fatal(value, polled)
	}
	if !strings.Contains(d.Display(value, "execution", nil), "1 pending") {
		t.Fatal(value)
	}
	d.Demand(nil)
	if d.Executions.Len() != 1 || d.pendingCount("7") != 1 {
		t.Fatal("uncertain receipt retired")
	}
	if posts.Load() != 2 {
		t.Fatal("read replayed submission")
	}
}

// Verify same scenario cannot overwrite receipts at capacity
func TestSameScenarioCannotOverwriteReceiptsAtCapacity(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	for range receiptLimit {
		if _, err := d.reserveExecution("7"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Command(context.Background(), "run", receiptSettings(7), 0); err == nil {
		t.Fatal("full same-scenario history allowed submission")
	}
	if posts.Load() != 0 || d.Executions.Len() != receiptLimit {
		t.Fatal("untracked POST", posts.Load(), d.Executions.Len())
	}
}

// Verify scenario identity must match before mutation
func TestScenarioIdentityMustMatchBeforeMutation(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	upstream := d.HTTP.Client.Transport
	d.HTTP.Client.Transport = receiptTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"scenario":{"id":8,"name":"Different scenario","teamId":1,"isActive":true}}`))}, nil
		}
		return upstream.RoundTrip(r)
	})
	for _, action := range []string{"run", "activate", "deactivate"} {
		if _, err := d.Command(context.Background(), action, receiptSettings(7), 0); err == nil {
			t.Fatal(action, "accepted another scenario")
		}
	}
	if posts.Load() != 0 || d.Executions.Len() != 0 {
		t.Fatal("mismatched scenario caused mutation", posts.Load(), d.Executions)
	}
}

// Verify numeric scenario aliases share history and refresh
func TestNumericScenarioAliasesShareHistoryAndRefresh(t *testing.T) {
	var posts atomic.Int64
	d := receiptDriver(&posts)
	defer d.Close()
	settings := receiptSettings(7)
	settings["scenario"] = "007"
	settings["team_id"] = "01"
	if d.Key(settings) != d.Key(receiptSettings(7)) {
		t.Fatal("aliases split resource state")
	}
	d.Demand([]map[string]any{settings})
	if _, err := d.Command(context.Background(), "run", settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if !d.execution("7").Terminal {
		t.Fatal("active aliased receipt was prematurely retired")
	}
	if _, err := d.Command(context.Background(), "execution", settings, 0); err != nil {
		t.Fatal(err)
	}
	if !d.execution("7").Refresh {
		t.Fatal("aliased refresh missed retained execution")
	}
	d.Demand(nil)
	if _, err := d.Read(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if d.Executions.Len() != 0 || posts.Load() != 1 {
		t.Fatal("retirement lost isolation", d.Executions, posts.Load())
	}
}
