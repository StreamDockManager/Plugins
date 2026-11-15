package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go/httptrack"
	"github.com/StreamDockManager/Plugin-SDK/Go/requests"
)

type weatherRequests struct {
	group    requests.Group[map[string]any]
	restored map[string]*forecastHTTPError
	closed   atomic.Bool
}

// Share a forecast read while preserving each subscriber cancellation
func (r *weatherRequests) get(ctx context.Context, endpoint string, client *http.Client) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.closed.Load() {
		return nil, context.Canceled
	}
	// Restored forecast metadata is immutable after initialization and bounded by the product cache
	if failure := r.restored[endpoint]; failure != nil && time.Now().Before(failure.RetryAt) {
		return nil, failure
	}
	return r.group.Get(ctx, endpoint, func(work context.Context) (map[string]any, error) { return fetchWeather(work, endpoint, client) }, func(err error) time.Duration {
		if failure, ok := errors.AsType[*forecastHTTPError](err); ok {
			return time.Until(failure.RetryAt)
		}
		return 0
	})
}

// Cancel pending forecast reads and close the shared request group
func (r *weatherRequests) close() { r.closed.Store(true); r.group.Close() }

// Fetch and decode a bounded forecast response
func fetchWeather(ctx context.Context, endpoint string, client *http.Client) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid weather request")
	}
	req.Header.Set("User-Agent", "stream-dock-manager-plugins/Weather")
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	response, err := httptrack.TrackHTTPClient(client).Do(req)
	if err != nil {
		if endpointError, ok := errors.AsType[*url.Error](err); ok {
			err = endpointError.Err
		}
		return nil, fmt.Errorf("weather request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var detail struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail)
		reason := strings.Join(strings.Fields(detail.Reason), " ")
		if len(reason) > 160 {
			reason = reason[:160]
		}
		return nil, &forecastHTTPError{Status: response.StatusCode, Reason: reason, RetryAt: forecastRetryAfter(response.Header.Get("Retry-After"), time.Now())}
	}
	var data map[string]any
	if err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// forecastHTTPError survives request coalescing and sampling so retry deadlines
// apply equally to automatic polling, manual refresh and every visible binding
type forecastHTTPError struct {
	Status  int
	Reason  string
	RetryAt time.Time
}

// Describe the forecast HTTP failure
func (e *forecastHTTPError) Error() string {
	text := fmt.Sprintf("weather service HTTP %d", e.Status)
	if e.Reason != "" {
		text += ": " + e.Reason
	}
	return text
}

// Parse the forecast provider retry deadline
func forecastRetryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil || (errors.Is(err, strconv.ErrRange) && onlyDigits(value)) {
		if seconds > uint64(math.MaxInt64/int64(time.Second)) {
			seconds = uint64(math.MaxInt64 / int64(time.Second))
		}
		if seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return deadline
	}
	return time.Time{}
}

// Recognize a nonempty unsigned decimal string
func onlyDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
