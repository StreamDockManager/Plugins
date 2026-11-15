package hiscores

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIDMappingLargeXPUnrankedAndProcessBaselines verifies ID mapping large XP unranked and process baselines
func TestIDMappingLargeXPUnrankedAndProcessBaselines(t *testing.T) {
	var xp int64 = 9007199254740993
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprintf(w, `{"skills":[{"id":4,"name":"Ranged","rank":-1,"level":-1,"xp":-1},{"id":0,"name":"Overall","rank":1,"level":2277,"xp":%d}],"activities":[{"id":100,"name":"Fixture boss","rank":-1,"score":-1}]}`, xp)
	}))
	defer server.Close()
	d := New(map[string]string{"normal": server.URL, "ironman": server.URL}, nil)
	defer d.Close()
	ctx := context.Background()
	s := map[string]any{"username": "Fixture", "board": "normal", "skill": "0", "metric": "xp"}
	first, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if first.(Sample).Delta["0"] != 0 || !strings.Contains(d.Display(first, "skill", s), "9007199254740993") {
		t.Fatal(first)
	}
	xp += 123
	second, err := d.Read(ctx, s)
	if err != nil || second.(Sample).Delta["0"] != 123 {
		t.Fatal(second, err)
	}
	before := requests
	if _, err = d.Command(ctx, "reset-baseline", s, 0); err != nil || requests != before {
		t.Fatal("reset fetched instead of using latest sample", err, requests)
	}
	third, err := d.Read(ctx, s)
	if err != nil || third.(Sample).Delta["0"] != 0 {
		t.Fatal(third, err)
	}
	s["board"] = "ironman"
	xp += 77
	value, err := d.Read(ctx, s)
	if err != nil || value.(Sample).Delta["0"] != 0 {
		t.Fatal("board baseline reused", value, err)
	}
	s["username"] = "Other"
	xp += 88
	value, err = d.Read(ctx, s)
	if err != nil || value.(Sample).Delta["0"] != 0 {
		t.Fatal("player baseline reused", value, err)
	}
	s["skill"] = "4"
	if text := d.Display(value, "skill", s); !strings.Contains(text, "Unranked") {
		t.Fatal(text)
	}
	s["skill"] = "0"
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	before = len(d.Latest)
	if _, err = d.Read(ctx, s); err == nil || len(d.Latest) != before {
		t.Fatal("cancelled sample committed", err)
	}
}

// TestMalformedAndChangedPositionalMappingsAreRejected verifies malformed and changed positional mappings are rejected
func TestMalformedAndChangedPositionalMappingsAreRejected(t *testing.T) {
	for _, raw := range []string{`{}`, `{"skills":[{"id":0,"name":"Overall","rank":1,"level":1}]}`, `{"skills":[{"id":0,"name":"Overall","rank":1,"level":1,"xp":0},{"id":0,"name":"Other","rank":1,"level":1,"xp":0}]}`, `{"skills":[{"id":0,"name":"Overall","rank":1,"level":1,"xp":1.5}]}`} {
		if _, err := ParseJSON([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	mapping := Mapping{Skills: []string{"Overall", "Necromancy"}, Activities: []string{"Activity 0"}}
	sample, err := ParseCSV([]byte("1,120,9007199254740993\n-1,-1,-1\n-1,42\n"), mapping)
	if err != nil || *sample.Skills["0"].XP != 9007199254740993 || *sample.Activities["0"].Score != 42 {
		t.Fatal(sample, err)
	}
	for _, raw := range []string{"1,120,0\n-1,-1,-1\n", "1,120,0\n-1,12\n-1,42\n", "1,120,no\n-1,-1,-1\n-1,42\n"} {
		if _, err := ParseCSV([]byte(raw), mapping); err == nil {
			t.Fatal(raw)
		}
	}
}

// TestHTTPBackoffAndDelayedCancellation verifies HTTP backoff and delayed cancellation
func TestHTTPBackoffAndDelayedCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("player") == "Slow" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer server.Close()
	d := New(map[string]string{"normal": server.URL}, nil)
	defer d.Close()
	s := map[string]any{"username": "Limited"}
	if _, err := d.Read(context.Background(), s); err == nil {
		t.Fatal("rate limit accepted")
	}
	s["username"] = "Slow"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.Read(ctx, s); err == nil || len(d.Baseline) != 0 {
		t.Fatal("obsolete sample changed baseline", err)
	}
}

// TestJSONFirstCSVRetryIsLimitedToNotFound verifies JSON first CSV retry is limited to not found
func TestJSONFirstCSVRetryIsLimitedToNotFound(t *testing.T) {
	legacy := 0
	code := 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.WriteHeader(code)
			return
		}
		legacy++
		fmt.Fprint(w, "1,2,9007199254740993\n-1,42\n")
	}))
	defer server.Close()
	mapping := Mapping{Skills: []string{"Overall"}, Activities: []string{"Gapped activity"}, ActivityIDs: []int{17}}
	d := New(map[string]string{"normal": server.URL + "/json"}, &mapping)
	d.FallbackBoards = map[string]string{"normal": server.URL + "/csv"}
	defer d.Close()
	s := map[string]any{"username": "Fixture"}
	value, err := d.Read(context.Background(), s)
	if err != nil || *value.(Sample).Activities["17"].Score != 42 || legacy != 1 {
		t.Fatal(value, err, legacy)
	}
	for _, status := range []int{200, 400, 401, 403, 429, 500} {
		code = status
		// 200 has an empty/malformed JSON body; none of these failures may retry CSV
		if _, err = d.Read(context.Background(), s); err == nil || legacy != 1 {
			t.Fatal("non-404 or malformed JSON triggered CSV retry", status, err, legacy)
		}
	}
}
