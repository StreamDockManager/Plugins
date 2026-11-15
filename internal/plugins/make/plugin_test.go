package makeplugin

import (
	"context"
	"fmt"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Verify discovery async execution and uncertain run is never replayed
func TestDiscoveryAsyncExecutionAndUncertainRunIsNeverReplayed(t *testing.T) {
	posts := 0
	terminal := false
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token fixture-secret" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/scenarios":
			if r.URL.Query().Get("teamId") != "4" {
				t.Error("team missing")
			}
			fmt.Fprint(w, `{"scenarios":[{"id":7,"name":"Fixture","teamId":4,"isActive":true}]}`)
		case r.Method == "GET" && r.URL.Path == "/scenarios/7":
			fmt.Fprint(w, `{"scenario":{"id":7,"name":"Fixture","teamId":4,"isActive":true}}`)
		case r.Method == "POST" && r.URL.Path == "/scenarios/7/run":
			posts++
			if fail {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"executionId":"execution1"}`)
		case r.Method == "GET" && r.URL.Path == "/scenarios/7/logs/execution1":
			if terminal {
				fmt.Fprint(w, `{"scenarioLog":{"id":"execution1","status":1}}`)
			} else {
				fmt.Fprint(w, `{"scenarioLog":{"id":"execution1","displayStatus":"RUNNING"}}`)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL}
	defer d.Close()
	s := map[string]any{"team_id": "4", "token": "fixture-secret", "scenario": "7", "inputs": `{"value":true}`}
	ctx := context.Background()
	choices, err := d.Discover(ctx, s)
	if err != nil || len(choices) != 1 || posts != 0 {
		t.Fatal(choices, err, posts)
	}
	if _, err = d.Read(ctx, s); err != nil || posts != 0 {
		t.Fatal("read-only test mutated scenario", err)
	}
	outcome, err := d.Command(ctx, "run", s, 0)
	if err != nil || !strings.Contains(outcome.Message, "pending") {
		t.Fatal(outcome, err)
	}
	value, err := d.Read(ctx, s)
	if err != nil || !strings.Contains(d.Display(value, "execution", s), "RUNNING") {
		t.Fatal(value, err)
	}
	terminal = true
	value, err = d.Read(ctx, s)
	if err != nil || !strings.Contains(d.Display(value, "execution", s), "Completed successfully") {
		t.Fatal(value, err)
	}
	fail = true
	if _, err = d.Command(ctx, "run", s, 0); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal(err)
	}
	for range 3 {
		value, err = d.Read(ctx, s)
		if err != nil || !strings.Contains(d.Display(value, "execution", s), "uncertain") {
			t.Fatal(value, err)
		}
	}
	if posts != 2 {
		t.Fatal("run automatically resubmitted", posts)
	}
}

// Verify webhook acceptance and no read only trigger
func TestWebhookAcceptanceAndNoReadOnlyTrigger(t *testing.T) {
	posts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; fmt.Fprint(w, "Accepted") }))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP()}
	d.HTTP.Client.Transport = server.Client().Transport
	defer d.Close()
	s := map[string]any{"_action": "webhook", "webhook": server.URL, "inputs": "{}"}
	ctx := context.Background()
	if value, err := d.Read(ctx, s); err != nil || posts != 0 {
		t.Fatal(err, posts)
	} else if text := value.(Sample).TestResult(); !strings.Contains(text, "no request was sent") {
		t.Fatal("webhook check inferred execution", text)
	}
	outcome, err := d.Command(ctx, "webhook", s, 0)
	if err != nil || posts != 1 || !strings.Contains(outcome.Message, "completion unknown") {
		t.Fatal(outcome, err, posts)
	}
	for range 3 {
		if _, err = d.Read(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 1 {
		t.Fatal("webhook replayed", posts)
	}
}

// Verify malformed and unauthorized responses
func TestMalformedAndUnauthorizedResponses(t *testing.T) {
	for _, status := range []int{200, 401, 429} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, `{}`) }))
		d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL}
		if _, err := d.Read(context.Background(), map[string]any{"scenario": "1", "team_id": "1", "token": "secret"}); err == nil {
			t.Fatal(status)
		}
		d.Close()
		server.Close()
	}
}

// Verify terminal execution stops log polling until explicit refresh
func TestTerminalExecutionStopsLogPollingUntilExplicitRefresh(t *testing.T) {
	for _, status := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			logs, scenarios, runs := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/scenarios/7":
					scenarios++
					fmt.Fprint(w, `{"scenario":{"id":7,"name":"Fixture","teamId":4,"isActive":true}}`)
				case "/scenarios/7/run":
					runs++
					fmt.Fprint(w, `{"executionId":"execution1"}`)
				case "/scenarios/7/logs/execution1":
					logs++
					fmt.Fprintf(w, `{"scenarioLog":{"id":"execution1","status":%d}}`, status)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL, Executions: seededExecutions(map[string]Execution{"7": {ID: "execution1"}})}
			defer d.Close()
			s := map[string]any{"scenario": "7", "team_id": "4", "token": "fixture", "inputs": "{}"}
			ctx := context.Background()
			for range 4 {
				if _, err := d.Read(ctx, s); err != nil {
					t.Fatal(err)
				}
			}
			if logs != 1 || scenarios != 4 {
				t.Fatalf("terminal execution continued polling: logs=%d scenario=%d", logs, scenarios)
			}
			if _, err := d.Command(ctx, "execution", s, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Read(ctx, s); err != nil {
				t.Fatal(err)
			}
			if logs != 2 {
				t.Fatal("explicit refresh did not read log", logs)
			}
			if _, err := d.Command(ctx, "run", s, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Read(ctx, s); err != nil {
				t.Fatal(err)
			}
			if logs != 3 || runs != 1 {
				t.Fatalf("new execution did not resume polling: logs=%d runs=%d", logs, runs)
			}
		})
	}
}

// Verify missing and pending execution logs keep polling
func TestMissingAndPendingExecutionLogsKeepPolling(t *testing.T) {
	logs := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/logs/") {
			logs++
			if logs == 1 {
				w.WriteHeader(404)
				return
			}
			if logs == 2 {
				fmt.Fprint(w, `{"scenarioLog":{"id":"execution1","displayStatus":"RUNNING"}}`)
				return
			}
			fmt.Fprint(w, `{"scenarioLog":{"id":"execution1","status":1}}`)
			return
		}
		fmt.Fprint(w, `{"scenario":{"id":7,"name":"Fixture","teamId":4,"isActive":true}}`)
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL, Executions: seededExecutions(map[string]Execution{"7": {ID: "execution1"}})}
	defer d.Close()
	settings := map[string]any{"scenario": "7", "team_id": "4", "token": "fixture"}
	for range 5 {
		if _, err := d.Read(context.Background(), settings); err != nil {
			t.Fatal(err)
		}
	}
	if logs != 3 || !d.executionReceipt("7").Terminal {
		t.Fatal("pending/404 did not reach stable terminal result", logs, d.Executions)
	}
}
