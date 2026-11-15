package nominatimbroker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// request issues the fixture broker request
func request(b *Broker, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest("GET", "/search?"+query, nil))
	return w
}

// assertCacheBytes checks the broker byte charge against its retained entries
func assertCacheBytes(t *testing.T, b *Broker) {
	t.Helper()
	size := 0
	for key, item := range b.cache {
		size += len(key) + cap(item.data)
	}
	if size != b.bytes || size > cacheByteLimit || len(b.cache) > cacheEntryLimit {
		t.Fatalf("cache accounting: actual=%d recorded=%d entries=%d", size, b.bytes, len(b.cache))
	}
}

// TestBrokerCacheBytePlateauLRUExpiryAndThrottle verifies broker cache byte plateau lru expiry and throttle
func TestBrokerCacheBytePlateauLRUExpiryAndThrottle(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	payload := `[{"value":"` + strings.Repeat("x", (256<<10)-14) + `"}]`
	b := &Broker{now: func() time.Time { return now }, Config: func() (Config, error) {
		return Config{Upstream: "https://local.example/search"}, nil
	}, Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}}
	hot := "postalcode=00000&countrycodes=us"
	if got := request(b, hot).Code; got != 200 {
		t.Fatal(got)
	}
	peak, peakEntries := b.bytes, len(b.cache)
	for i := 1; i < 1024; i++ {
		before := b.next
		if got := request(b, hot).Code; got != 200 || b.next != before {
			t.Fatal("hot cached lookup changed upstream throttle", got)
		}
		query := fmt.Sprintf("postalcode=%05d&countrycodes=us", i)
		if got := request(b, query).Code; got != 429 || calls != i {
			t.Fatal("eviction bypassed throttle", got, calls)
		}
		now = now.Add(1100 * time.Millisecond)
		if got := request(b, query).Code; got != 200 || calls != i+1 {
			t.Fatal(got, calls)
		}
		assertCacheBytes(t, b)
		peak, peakEntries = max(peak, b.bytes), max(peakEntries, len(b.cache))
	}
	if got := request(b, hot).Code; got != 200 || calls != 1024 {
		t.Fatal("hot LRU value evicted", got, calls)
	}
	if got := request(b, "postalcode=00001&countrycodes=us").Code; got != 429 || calls != 1024 {
		t.Fatal("cold value was not evicted or eviction bypassed throttle", got, calls)
	}
	now = now.Add(cacheTTL)
	b.next = now.Add(time.Minute)
	if got := request(b, hot).Code; got != 429 || calls != 1024 || len(b.cache) != 0 || b.bytes != 0 {
		t.Fatal("expired bodies retained or expiry bypassed throttle", got, calls, b.bytes)
	}
	t.Logf("1,024 maximum responses plus hot lookups: peak_bytes=%d peak_entries=%d expired_bytes=%d upstream_calls=%d", peak, peakEntries, b.bytes, calls)
}

// TestBrokerCountLimitEvictsOneLRUEntryWithoutClearing verifies broker count limit evicts one lru entry without clearing
func TestBrokerCountLimitEvictsOneLRUEntryWithoutClearing(t *testing.T) {
	b := &Broker{}
	now := time.Now()
	for i := range cacheEntryLimit {
		b.remember(fmt.Sprintf("%04d", i), []byte(`[]`), now)
	}
	// Protect the oldest entry with a later access stamp
	item := b.cache["0000"]
	b.access++
	item.used = b.access
	b.cache["0000"] = item
	b.remember("new", []byte(`[]`), now)
	if len(b.cache) != cacheEntryLimit || b.cache["0000"].data == nil || b.cache["new"].data == nil || b.cache["0001"].data != nil {
		t.Fatal("entry limit did not preserve hot cache", len(b.cache))
	}
	assertCacheBytes(t, b)
	b.expire(now.Add(cacheTTL))
	if len(b.cache) != 0 || b.bytes != 0 {
		t.Fatal("TTL boundary did not expire entries")
	}
}

// TestBrokerIdleExpiryAndCleanupCancellation verifies broker idle expiry and cleanup cancellation
func TestBrokerIdleExpiryAndCleanupCancellation(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	b := &Broker{now: func() time.Time { return time.Unix(0, clock.Load()) }}
	b.remember("postal", make([]byte, 256<<10), b.timeNow())
	clock.Add(int64(cacheTTL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { b.cleanup(ctx, ticks); close(done) }()
	// A blocked upstream must not make the cleanup loop wait for the lock
	b.mu.Lock()
	ticks <- b.timeNow()
	ticks <- b.timeNow() // Receiving another tick proves the first was skipped
	b.mu.Unlock()
	ticks <- b.timeNow()
	ticks <- b.timeNow() // The prior cleanup completed before this receive
	b.mu.Lock()
	if b.bytes != 0 || len(b.cache) != 0 {
		t.Error("idle expired cache retained", b.bytes)
	}
	b.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not stop")
	}
}

// TestBrokerBusyPrivacyAndRetryAfterWithClock verifies broker busy privacy and retry after with clock
func TestBrokerBusyPrivacyAndRetryAfterWithClock(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	status := 200
	retry := ""
	calls := 0
	b := &Broker{now: func() time.Time { return now }, Config: func() (Config, error) {
		return Config{Upstream: "https://private.example/search", AllowAddresses: true}, nil
	}, Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {retry}}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}}
	b.mu.Lock()
	w := request(b, "postalcode=00000&countrycodes=us")
	b.mu.Unlock()
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || calls != 0 {
		t.Fatal("busy request did not fail fast")
	}
	for i := range 2 {
		now = now.Add(1100 * time.Millisecond)
		if got := request(b, "q=synthetic+private+address").Code; got != 200 || calls != i+1 || len(b.cache) != 0 || b.bytes != 0 {
			t.Fatal("personal address cached", got, calls, b.bytes)
		}
	}
	for _, code := range []int{429, 503} {
		now = now.Add(3 * time.Minute).Truncate(time.Second)
		status = code
		retry = now.Add(120 * time.Second).Format(http.TimeFormat)
		w = request(b, "postalcode=00000&countrycodes=us")
		if w.Code != code || w.Header().Get("Retry-After") != "120" || b.next != now.Add(120*time.Second) {
			t.Fatal("Retry-After date changed", w.Code, w.Header(), b.next)
		}
		before := calls
		if got := request(b, "postalcode=00001&countrycodes=us").Code; got != 429 || calls != before {
			t.Fatal("upstream cooldown ignored")
		}
	}
}

// TestBrokerInvalidResponsesNeverConsumeCacheBudget verifies broker invalid responses never consume cache budget
func TestBrokerInvalidResponsesNeverConsumeCacheBudget(t *testing.T) {
	now := time.Now()
	payload := ""
	b := &Broker{now: func() time.Time { return now }, Config: func() (Config, error) {
		return Config{Upstream: "https://local.example/search"}, nil
	}, Client: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}}
	for _, invalid := range []string{"null", "{}", "[", "[" + strings.Repeat("{},", 40) + "{}]", `[{"value":"` + strings.Repeat("x", 256<<10) + `"}]`} {
		payload = invalid
		now = now.Add(1100 * time.Millisecond)
		if got := request(b, "postalcode=00000&countrycodes=us").Code; got != 502 || b.bytes != 0 || len(b.cache) != 0 {
			t.Fatal("invalid response cached", got, b.bytes)
		}
	}
}
