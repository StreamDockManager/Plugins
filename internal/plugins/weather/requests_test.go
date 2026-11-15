package weather

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type joinedReadContext struct {
	context.Context
	ready chan struct{}
	once  sync.Once
}

// Signal that a subscriber joined before forwarding its cancellation channel
func (c *joinedReadContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.ready) })
	return c.Context.Done()
}

// Verify weather requests share endpoint and isolate cancellation
func TestWeatherRequestsShareEndpointAndIsolateCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			fmt.Fprint(w, `{"current":{"temperature_2m":20}}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	requests := &weatherRequests{}
	defer requests.close()
	first, cancel := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	go func() { _, err := requests.get(first, server.URL, nil); done1 <- err }()
	<-started
	joined := &joinedReadContext{Context: context.Background(), ready: make(chan struct{})}
	go func() { _, err := requests.get(joined, server.URL, nil); done2 <- err }()
	select {
	case <-joined.ready:
	case <-time.After(time.Second):
		t.Fatal("second consumer did not join")
	}
	cancel()
	if err := <-done1; err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	if err := <-done2; err != nil {
		t.Fatal("cancelled first consumer cancelled shared request", err)
	}
	if count.Load() != 1 {
		t.Fatal("duplicate endpoint request", count.Load())
	}
}

// Verify weather all consumers retired cancel request and allow retry
func TestWeatherAllConsumersRetiredCancelRequestAndAllowRetry(t *testing.T) {
	started := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer server.Close()
	requests := &weatherRequests{}
	defer requests.close()
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := requests.get(ctx, server.URL, nil); done <- err }()
		<-started
		cancel()
		if err := <-done; err != context.Canceled {
			t.Fatal(err)
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("orphan weather request continued")
		}
	}
}

// Verify weather requests separate endpoints and do not cache failures
func TestWeatherRequestsSeparateEndpointsAndDoNotCacheFailures(t *testing.T) {
	var first, second atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("units") == "c" {
			if first.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
		} else {
			second.Add(1)
		}
		fmt.Fprint(w, `{"current":{"temperature_2m":20}}`)
	}))
	defer server.Close()
	requests := &weatherRequests{}
	defer requests.close()
	if _, err := requests.get(context.Background(), server.URL+"?units=c", nil); err == nil {
		t.Fatal("HTTP error accepted")
	}
	if _, err := requests.get(context.Background(), server.URL+"?units=c", nil); err != nil {
		t.Fatal("failure poisoned retry", err)
	}
	if _, err := requests.get(context.Background(), server.URL+"?units=f", nil); err != nil {
		t.Fatal(err)
	}
	if first.Load() != 2 || second.Load() != 1 {
		t.Fatal("endpoint isolation failed", first.Load(), second.Load())
	}
}

// Verify distinct weather endpoints run concurrently
func TestDistinctWeatherEndpointsRunConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.RawQuery
		select {
		case <-release:
			fmt.Fprint(w, `{"current":{"temperature_2m":20}}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	requests := &weatherRequests{}
	defer requests.close()
	cancelled, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := requests.get(cancelled, server.URL+"?units=c", nil); first <- err }()
	go func() { _, err := requests.get(context.Background(), server.URL+"?units=f", nil); second <- err }()
	observed := map[string]bool{}
	for range 2 {
		select {
		case key := <-started:
			observed[key] = true
		case <-time.After(time.Second):
			t.Fatal("different endpoint was blocked")
		}
	}
	if len(observed) != 2 {
		t.Fatal("endpoints coalesced together", observed)
	}
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal("other endpoint cancelled", err)
	}
}
