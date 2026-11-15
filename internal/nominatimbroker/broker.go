// Package nominatimbroker centralizes the public service's application-wide
// request budget. Run one instance (or put a distributed limiter in front)
package nominatimbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cacheByteLimit = 16 << 20
const cacheEntryLimit = 1024
const cacheTTL = 24 * time.Hour

type Config struct {
	Upstream       string `json:"upstream"`
	AllowAddresses bool   `json:"allow_addresses"`
}
type entry struct {
	data []byte
	at   time.Time
	used uint64
}
type Broker struct {
	Config func() (Config, error)
	Client *http.Client
	mu     sync.Mutex
	cache  map[string]entry
	next   time.Time
	bytes  int
	access uint64
	now    func() time.Time
}

// timeNow returns the clock used by broker expiration
func (b *Broker) timeNow() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// RunCleanup releases expired postal responses even when no searches arrive
// The service owns this loop; cancellation stops its ticker and returns
func (b *Broker) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	b.cleanup(ctx, ticker.C)
}

// cleanup removes expired broker entries
func (b *Broker) cleanup(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			// A slow upstream owns the lock for at most the request timeout
			// Skip this tick instead of adding another waiter to interactive work
			if b.mu.TryLock() {
				b.expire(b.timeNow())
				b.mu.Unlock()
			}
		}
	}
}

// remove removes a broker entry and updates its byte charge
func (b *Broker) remove(key string) {
	item := b.cache[key]
	b.bytes -= len(key) + cap(item.data)
	delete(b.cache, key)
}

// expire evicts cache entries whose deadline has elapsed
func (b *Broker) expire(now time.Time) {
	for key, item := range b.cache {
		if !now.Before(item.at.Add(cacheTTL)) {
			b.remove(key)
		}
	}
}

// Count response backing buffers and keys; entry metadata has a separate count
// bound. A cache hit changes recency but never extends its 24-hour lifetime
func (b *Broker) remember(key string, data []byte, now time.Time) {
	b.expire(now)
	if _, ok := b.cache[key]; ok {
		b.remove(key)
	}
	size := len(key) + cap(data)
	if size > cacheByteLimit {
		return
	}
	for len(b.cache) >= cacheEntryLimit || b.bytes+size > cacheByteLimit {
		oldest := ""
		for candidate, item := range b.cache {
			if oldest == "" || item.used < b.cache[oldest].used || (item.used == b.cache[oldest].used && candidate < oldest) {
				oldest = candidate
			}
		}
		b.remove(oldest)
	}
	if b.cache == nil {
		b.cache = map[string]entry{}
	}
	b.access++
	b.cache[key] = entry{data: data, at: now, used: b.access}
	b.bytes += size
}

// public recognizes the public OpenStreetMap service hostname
func public(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "openstreetmap.org" || strings.HasSuffix(host, ".openstreetmap.org")
}

// ServeHTTP serves a bounded geocoding request through the broker
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet || r.URL.Path != "/search" {
		http.Error(w, "GET /search required", http.StatusNotFound)
		return
	}
	cfg, err := b.Config()
	if err != nil {
		http.Error(w, "Service configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream, err := url.Parse(cfg.Upstream)
	if err != nil || upstream.Hostname() == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || (upstream.Scheme != "https" && !(upstream.Scheme == "http" && upstream.Hostname() == "127.0.0.1")) {
		http.Error(w, "Invalid upstream configuration", http.StatusServiceUnavailable)
		return
	}
	input := r.URL.Query()
	q := url.Values{"format": {"jsonv2"}, "addressdetails": {"1"}, "limit": {"5"}}
	country := strings.ToLower(strings.TrimSpace(input.Get("countrycodes")))
	if country != "" && (len(country) != 2 || country[0] < 'a' || country[0] > 'z' || country[1] < 'a' || country[1] > 'z') {
		http.Error(w, "Country must be a two-letter code", 400)
		return
	}
	if country != "" {
		q.Set("countrycodes", country)
	}
	postal, address := strings.TrimSpace(input.Get("postalcode")), strings.TrimSpace(input.Get("q"))
	if address != "" {
		if public(upstream.Hostname()) || !cfg.AllowAddresses {
			http.Error(w, "Address searches require a private/contracted upstream", 400)
			return
		}
		if postal != "" || len(address) > 512 {
			http.Error(w, "Invalid address query", 400)
			return
		}
		q.Set("q", address)
	} else {
		if country == "" || postal == "" || len(postal) > 20 {
			http.Error(w, "Postal code and country required", 400)
			return
		}
		for _, c := range postal {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == ' ' || c == '-') {
				http.Error(w, "Invalid postal code", 400)
				return
			}
		}
		q.Set("postalcode", postal)
	}
	upstream.RawQuery = q.Encode()
	key := upstream.String()
	// Fail fast instead of accumulating an unbounded queue of interactive requests
	if !b.mu.TryLock() {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Search service busy", http.StatusTooManyRequests)
		return
	}
	defer b.mu.Unlock()
	now := b.timeNow()
	b.expire(now)
	if cached, ok := b.cache[key]; ok {
		b.access++
		cached.used = b.access
		b.cache[key] = cached
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached.data)
		return
	}
	if now.Before(b.next) {
		w.Header().Set("Retry-After", fmt.Sprint(max(1, int(b.next.Sub(now).Seconds())+1)))
		http.Error(w, "Search service busy", http.StatusTooManyRequests)
		return
	}
	b.next = now.Add(1100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
	req.Header.Set("User-Agent", "StreamDockManager-Plugins/0.1.0 Weather (+https://github.com/StreamDockManager/Plugins)")
	req.Header.Set("Accept", "application/json")
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	owned := *client
	// Keep address queries on the reviewed upstream rather than following redirects
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := owned.Do(req)
	if err != nil {
		http.Error(w, "Upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 429 || response.StatusCode == 503 {
			wait := time.Minute
			if seconds, err := time.ParseDuration(response.Header.Get("Retry-After") + "s"); err == nil && seconds > 0 {
				wait = seconds
			} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && at.After(b.timeNow()) {
				wait = at.Sub(b.timeNow())
			}
			b.next = b.timeNow().Add(wait)
			w.Header().Set("Retry-After", fmt.Sprint(max(1, int(wait.Seconds()))))
		}
		http.Error(w, "Upstream unavailable", response.StatusCode)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	var rows []json.RawMessage
	if err != nil || len(data) > 256<<10 || json.Unmarshal(data, &rows) != nil || rows == nil || len(rows) > 40 {
		http.Error(w, "Invalid upstream response", http.StatusBadGateway)
		return
	}
	// Cache only nonpersonal postal searches, including negative results
	if address == "" {
		b.remember(key, data, b.timeNow())
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
