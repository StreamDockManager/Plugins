package hiscores

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit/peers"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// TestSavedPlayerRecoverySharedReadsAndObsoleteRequests verifies saved player recovery shared reads and obsolete requests
func TestSavedPlayerRecoverySharedReadsAndObsoleteRequests(t *testing.T) {
	var reads atomic.Int32
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		player := r.URL.Query().Get("player")
		if player == "Slow" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		if player == "Missing" {
			w.WriteHeader(404)
			return
		}
		level := 80
		if r.URL.Path == "/ironman" {
			level = 81
		}
		fmt.Fprintf(w, `{"skills":[{"id":0,"name":"Overall","rank":1,"level":%d,"xp":100},{"id":1,"name":"Attack","rank":2,"level":%d,"xp":100}],"activities":[]}`, level, level)
	}))
	defer server.Close()
	game := Game{Slug: "fixture", Skills: []Skill{
		{ID: "0", Name: "Overall", Cap: 99, Cape: "hiscores/fixture/cape-overall", Icon: "hiscores/fixture/icon-overall", CapeSize: [2]int{131, 255}, IconSize: [2]int{25, 25}},
		{ID: "1", Name: "Attack", Cap: 99, Cape: "hiscores/fixture/cape-attack", Icon: "hiscores/fixture/icon-attack", CapeSize: [2]int{131, 255}, IconSize: [2]int{25, 25}},
	}}
	def, visuals := c.Define("fixture", "Hiscores", map[string]string{"skill": "plus"}, "skill")
	newEngine := func() (*engine.Engine, *testkit.Recorder) {
		e := fixtureengine.New(def, filepath.Join(t.TempDir(), "state.json"), Factory(game, c.Options{Definition: def, Visuals: visuals, Interval: 5 * time.Minute, New: func(chan<- struct{}) c.Driver {
			d := New(map[string]string{"normal": server.URL, "ironman": server.URL + "/ironman"}, nil)
			d.Game = &game
			return d
		}}))
		info, _ := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1"]}}`))
		r := testkit.New(info)
		t.Cleanup(func() { _ = e.Close() })
		return e, r
	}
	e, r := newEngine()
	ctx := context.Background()
	send := func(event, id, player, board string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"settings": map[string]any{"username": player, "board": board, "skill": "1"}})
		if err := e.Handle(ctx, r.Client, sdk.Event{Event: event, Context: id, Action: "app.streamdockmanager.plugins.fixture.skill", Payload: payload}); err != nil {
			t.Fatal(err)
		}
		r.Drain()
	}
	poll := func() {
		t.Helper()
		if err := e.Poll(ctx, r.Client); err != nil {
			t.Fatal(err)
		}
		peers.Eventually(t, func() bool {
			if err := e.DrainJobs(ctx, r.Client); err != nil {
				t.Fatal(err)
			}
			r.Drain()
			return len(e.Jobs) == 0
		})
	}
	status := func(id string) engine.DisplayStatus { return e.CollectStatus(ctx, id, e.Bindings[id]) }
	send("willAppear", "a", "", "normal")
	poll()
	if got := status("a"); got.Text != "Set player" || !got.Unavailable || reads.Load() != 0 {
		t.Fatal(got, reads.Load())
	}
	send("didReceiveSettings", "a", "Echolocate", "normal")
	poll()
	if got := status("a"); got.Text != "Attack\n80/99" || got.Unavailable {
		t.Fatal(got)
	}
	view := e.DisplayView("a", status("a"))
	if view.Feedback["value"] != "80/99" || view.Image != "hiscores/fixture/cape-attack.png" {
		t.Fatal(view)
	}
	send("willAppear", "b", "Echolocate", "normal")
	poll()
	if reads.Load() != 1 {
		t.Fatal("bindings did not share a fetched snapshot", reads.Load())
	}
	send("didReceiveSettings", "a", "Echolocate", "ironman")
	poll()
	if got := status("a"); got.Text != "Attack\n81/99" {
		t.Fatal(got)
	}
	send("willDisappear", "b", "Echolocate", "normal")
	send("didReceiveSettings", "a", "Slow", "normal")
	if err := e.Poll(ctx, r.Client); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow request did not start")
	}
	send("didReceiveSettings", "a", "Missing", "normal")
	poll()
	if got := status("a"); got.Text != "Player not found" || !got.Unavailable {
		t.Fatal(got)
	}
	send("didReceiveSettings", "a", "Echolocate", "normal")
	poll()
	if got := status("a"); !strings.Contains(got.Text, "80/99") {
		t.Fatal(got)
	}
	// A new process receives saved settings rather than a previous error/baseline
	e, r = newEngine()
	send("willAppear", "a", "Echolocate", "normal")
	poll()
	if got := status("a"); got.Text != "Attack\n80/99" {
		t.Fatal(got)
	}
}

// TestPlayerErrorsPreserveHTTPBackoffAndInspectorDetail verifies player errors preserve HTTP backoff and inspector detail
func TestPlayerErrorsPreserveHTTPBackoffAndInspectorDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "600"); w.WriteHeader(429) }))
	defer server.Close()
	d := New(map[string]string{"normal": server.URL}, nil)
	defer d.Close()
	for _, tc := range []struct{ player, label string }{{"", "Set player"}, {"account@example.com", "Check name"}, {"Echolocate", "Retry later"}} {
		s := map[string]any{"username": tc.player}
		_, err := d.Read(context.Background(), s)
		if err == nil || !strings.HasPrefix(err.Error(), tc.label+"\n") {
			t.Fatal(err)
		}
		safe := c.SafeError(err, s)
		if safe.Error() != err.Error() {
			t.Fatal("inspector explanation lost", safe)
		}
		if tc.label == "Retry later" && !c.RetryAt(safe).After(time.Now().Add(9*time.Minute)) {
			t.Fatal("Retry-After lost", safe)
		}
	}
	if d.Key(map[string]any{"username": " A_b\u00a0C "}) != d.Key(map[string]any{"username": "a b c"}) {
		t.Fatal("player normalization differs between request and cache key")
	}
}
