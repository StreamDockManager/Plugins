package internetspeedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

type localSpeedTransport struct {
	endpoint *url.URL
	next     http.RoundTripper
}

// Delegate the fixture HTTP request to its injected response handler
func (t localSpeedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/api/ios-config.php" && !strings.HasPrefix(request.URL.Path, "/speedtest/") {
		return nil, fmt.Errorf("unexpected fixture path %s", request.URL.Path)
	}
	copy := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = t.endpoint.Scheme, t.endpoint.Host
	copy.URL, copy.Host = &target, target.Host
	return t.next.RoundTrip(copy)
}

// Verify speed cancellation does not start later transfer
func TestSpeedCancellationDoesNotStartLaterTransfer(t *testing.T) {
	for _, phase := range []string{"latency-complete", "download-active", "download-complete", "upload-active"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var downloads, uploads atomic.Int64
			var baseURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/ios-config.php":
					fmt.Fprintf(w, `<settings><servers><server url="%s/speedtest/upload.php" id="fixture"/></servers></settings>`, baseURL)
				case r.URL.Path == "/speedtest/latency.txt":
					_, _ = io.WriteString(w, "test=test")
				case strings.HasPrefix(r.URL.Path, "/speedtest/random"):
					downloads.Add(1)
					if phase == "download-active" {
						cancel()
						return
					}
					_, _ = io.WriteString(w, strings.Repeat("x", 4096))
				case r.URL.Path == "/speedtest/upload.php":
					uploads.Add(1)
					if phase == "upload-active" && r.Method == http.MethodPost {
						cancel()
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
				default:
					t.Errorf("unexpected local request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			baseURL = server.URL
			endpoint, _ := url.Parse(baseURL)
			doer := server.Client()
			doer.Transport = cancelledSpeedTransport{localSpeedTransport{endpoint, doer.Transport}}
			client := speedtest.New(speedtest.WithUserConfig(&speedtest.UserConfig{UserAgent: speedtest.DefaultUserAgent}), speedtest.WithDoer(doer))
			client.SetCaptureTime(250 * time.Millisecond).SetRateCaptureFrequency(10 * time.Millisecond).SetNThread(1)
			var reachedUpload, completed bool
			var chunksBeforeUpload int
			_, err := runSpeedTest(ctx, "fixture", func(update SpeedUpdate) {
				if update.Action == "upload" && !update.Complete {
					chunksBeforeUpload = len(*client.Manager.(*speedtest.DataManager).Snapshot)
				}
				reachedUpload = reachedUpload || update.Action == "upload"
				completed = completed || update.Text == "Complete · 100%"
				if phase == "latency-complete" && update.Action == "latency" && update.Complete || phase == "download-complete" && update.Action == "download" && update.Complete {
					cancel()
				}
			}, client)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if completed || phase != "upload-active" && (uploads.Load() != 0 || reachedUpload) {
				t.Fatalf("cancelled session advanced: uploads=%d, upload progress=%v, complete=%v", uploads.Load(), reachedUpload, completed)
			}
			if phase == "upload-active" {
				chunks := len(*client.Manager.(*speedtest.DataManager).Snapshot) - chunksBeforeUpload
				if uploads.Load() != 2 || chunks > 30 {
					t.Fatalf("cancelled upload retry retention: requests=%d chunks=%d", uploads.Load(), chunks)
				}
			}
			if phase == "latency-complete" && downloads.Load() != 0 {
				t.Fatal("download started after latency cancellation")
			}
			if phase != "latency-complete" && downloads.Load() == 0 {
				t.Fatal("download cancellation was not exercised")
			}
		})
	}
}

type speedTransportFunc func(*http.Request) (*http.Response, error)

// Delegate the fixture HTTP request to its injected response handler
func (f speedTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// Verify cancelled speed transport never starts HTTP
func TestCancelledSpeedTransportNeverStartsHTTP(t *testing.T) {
	var calls int
	transport := cancelledSpeedTransport{speedTransportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/speedtest/latency.txt", nil)
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled request reached HTTP: calls=%d err=%v", calls, err)
	}
	request, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, request.URL.String(), nil)
	if _, err := transport.RoundTrip(request); err != nil || calls != 1 {
		t.Fatalf("live request did not reach HTTP: calls=%d err=%v", calls, err)
	}
}
