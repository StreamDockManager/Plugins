package httpwebhooks

import (
	"testing"
	"time"
)

// Verify automatic reads require explicit valid modern interval
func TestAutomaticReadsRequireExplicitValidModernInterval(t *testing.T) {
	e := &Plugin{}
	for _, settings := range []map[string]any{nil, {"interval": 30}, {"update_interval_ms": "1000"}, {"update_interval_ms": -1}, {"update_interval_ms": 0.5}, {"update_interval_ms": 500}} {
		if e.automaticRead("fixture", "request", settings) {
			t.Fatalf("invalid/obsolete settings authorized an automatic request: %v", settings)
		}
	}
	if !e.automaticRead("fixture", "request", map[string]any{"update_interval_ms": 0}) {
		t.Fatal("explicit Off must retain one initial read")
	}
	if pollingInterval("http-webhooks", "request", map[string]any{"update_interval_ms": 1000, "interval": 90}) != time.Second {
		t.Fatal("modern setting did not control cadence")
	}
}
