package weather

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Verify forecast retry after seconds dates and overflow
func TestForecastRetryAfterSecondsDatesAndOverflow(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Time
	}{{"120", now.Add(120 * time.Second)}, {now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)}, {"bogus", time.Time{}}, {"-1", time.Time{}}, {"0", time.Time{}}, {now.Add(-time.Hour).Format(http.TimeFormat), time.Time{}}} {
		if got := forecastRetryAfter(tc.header, now); !got.Equal(tc.want) {
			t.Fatal(tc, got)
		}
	}
	for _, value := range []string{"18446744073709551615", "18446744073709551616", strings.Repeat("9", 100)} {
		if got := forecastRetryAfter(value, now); got.Before(now.Add(100 * 365 * 24 * time.Hour)) {
			t.Fatal("overflow discarded or wrapped", value, got)
		}
	}
	for _, value := range []string{"+18446744073709551616", "-18446744073709551616", "18446744073709551616oops"} {
		if got := forecastRetryAfter(value, now); !got.IsZero() {
			t.Fatal("invalid retry value accepted", value, got)
		}
	}
}

// Verify forecast cooldown shared across manual refresh and sampler
func TestForecastCooldownSharedAcrossManualRefreshAndSampler(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"reason":"slow down"}`)
	}))
	defer server.Close()
	requests := &weatherRequests{}
	defer requests.close()
	_, err := requests.get(context.Background(), server.URL, nil)
	var failure *forecastHTTPError
	if !errors.As(err, &failure) || failure.Status != 429 || !failure.RetryAt.After(time.Now().Add(100*time.Second)) {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := requests.get(context.Background(), server.URL, nil); !errors.As(err, &failure) {
			t.Fatal(err)
		}
	}
	requests.group.CancelPending()
	if _, err := requests.get(context.Background(), server.URL, nil); !errors.As(err, &failure) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("manual refresh bypassed cooldown", calls.Load())
	}
	if next := weatherNextAttempt(time.Now(), 0, failure.RetryAt); !next.Equal(failure.RetryAt) {
		t.Fatal(next)
	}

}

// Verify sampler keeps HTTP failure and deadline across equivalent bindings
func TestSamplerKeepsHTTPFailureAndDeadlineAcrossEquivalentBindings(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		response := weatherResponse(503, `{"reason":"maintenance"}`)
		response.Header = http.Header{"Retry-After": []string{"120"}}
		return response, nil
	})}
	requests := &weatherRequests{}
	defer requests.close()
	request := statusRequest{binding: binding{Action: "weather.conditions", Settings: weatherSettings()}, weatherClient: client, weatherRequests: requests}
	first := request.sample(context.Background())
	if !first.Status.Unavailable || !strings.Contains(first.Status.Text, "503") || first.RetryAt.Before(time.Now().Add(100*time.Second)) {
		t.Fatal(first)
	}
	second := request.sample(context.Background())
	if calls.Load() != 1 || !second.RetryAt.Equal(first.RetryAt) {
		t.Fatal("binding bypassed resource cooldown", calls.Load(), second)
	}
}

// Verify retired weather request cannot install cooldown after replacement
func TestRetiredWeatherRequestCannotInstallCooldownAfterReplacement(t *testing.T) {
	requests := &weatherRequests{}
	defer requests.close()
	const endpoint = "https://weather.invalid/forecast"
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	client := &http.Client{Transport: weatherTransport(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			// A response can finish while cancellation or settings replacement
			// is waiting on the request lock. Let the retired response finish last
			<-release
			response := weatherResponse(429, `{"reason":"slow down"}`)
			response.Header = http.Header{"Retry-After": []string{"120"}}
			return response, nil
		}
		return weatherResponse(200, `{"current":{"temperature_2m":20}}`), nil
	})}
	first := make(chan error, 1)
	go func() { _, err := requests.get(context.Background(), endpoint, client); first <- err }()
	<-started
	requests.group.CancelPending()
	if _, err := requests.get(context.Background(), endpoint, client); err != nil {
		t.Fatal("replacement request failed", err)
	}
	close(release)
	<-first
	if _, err := requests.get(context.Background(), endpoint, client); err != nil {
		t.Fatal("retired response poisoned replacement", err)
	}
	if calls.Load() != 3 {
		t.Fatal("retired response retained cooldown", calls.Load())
	}
}
