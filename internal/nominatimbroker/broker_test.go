package nominatimbroker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

// RoundTrip returns the deterministic HTTP fixture response
func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }

// TestBrokerGlobalBudgetCachePrivacyAndRuntimeSwitch verifies broker global budget cache privacy and runtime switch
func TestBrokerGlobalBudgetCachePrivacyAndRuntimeSwitch(t *testing.T) {
	cfg := Config{Upstream: "https://nominatim.openstreetmap.org/search", AllowAddresses: true}
	calls := 0
	status := 200
	b := &Broker{Config: func() (Config, error) { return cfg, nil }, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}}
	request := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest("GET", "/search?"+query, nil))
		return w
	}
	if got := request("q=personal+address").Code; got != 400 || calls != 0 {
		t.Fatal(got, calls)
	}
	if got := request("postalcode=02108&countrycodes=us").Code; got != 200 || calls != 1 {
		t.Fatal(got, calls)
	}
	// Independent callers share one budget; identical cached searches bypass it
	if got := request("postalcode=10001&countrycodes=us").Code; got != 429 || calls != 1 {
		t.Fatal(got, calls)
	}
	if got := request("postalcode=02108&countrycodes=us").Code; got != 200 || calls != 1 {
		t.Fatal(got, calls)
	}
	cfg = Config{Upstream: "https://private.example/search", AllowAddresses: true}
	b.next = time.Time{}
	if got := request("q=permitted+address").Code; got != 200 || calls != 2 {
		t.Fatal(got, calls)
	}
	if len(b.cache) != 1 {
		t.Fatal("personal query cached")
	}
	b.next = time.Time{}
	status = 429
	w := request("postalcode=90210&countrycodes=us")
	if w.Code != 429 || w.Header().Get("Retry-After") != "120" {
		t.Fatal(w)
	}
	if time.Until(b.next) < 119*time.Second {
		t.Fatal("upstream cooldown lost")
	}
	cfg.Upstream = "https://other.example/search"
	b.next = time.Time{}
	status = 200
	if got := request("postalcode=02108&countrycodes=us").Code; got != 200 || calls != 4 {
		t.Fatal(got, calls)
	}
}
