package mqtt

import (
	"strings"
	"testing"
	"time"
)

// Verify oversized message replaces value with bounded receipt then recovers
func TestOversizedMessageReplacesValueWithBoundedReceiptThenRecovers(t *testing.T) {
	d := NewDriver(nil)
	d.Demand([]map[string]any{{"_action": "text", "topic": "sensor/#"}})
	d.receiveMessage(0, "sensor/a", []byte("old-value"), false)
	d.mu.Lock()
	old := d.snapshot("sensor/#")
	d.mu.Unlock()
	if !d.receiveMessage(0, "sensor/a", make([]byte, (64<<10)+1), true) {
		t.Fatal("receipt not recorded")
	}
	d.mu.Lock()
	sample := d.snapshot("sensor/#")
	d.mu.Unlock()
	message := sample["sensor/a"]
	if message.Text != "" || message.Rejection == "" || message.PayloadBytes != 65537 || time.Since(message.At) > time.Second {
		t.Fatal(message)
	}
	if old["sensor/a"].Text != "old-value" {
		t.Fatal("mutated published snapshot")
	}
	text := d.Display(Sample{Connected: true, Messages: sample}, "text", map[string]any{"topic": "sensor/#", "json_path": "value"})
	if !strings.Contains(text, "Rejected:") || !strings.Contains(text, "65537 bytes") || !strings.Contains(text, "received ") || strings.Contains(text, "old-value") {
		t.Fatal(text)
	}
	d.receiveMessage(0, "sensor/a", []byte("new-value"), false)
	if d.messages["sensor/a"].Rejection != "" || d.messages["sensor/a"].Text != "new-value" {
		t.Fatal("rejection did not recover")
	}
	cacheUsage(t, d)
}
