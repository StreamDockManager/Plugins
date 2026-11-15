package mqtt

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Build the MQTT subscription filters used by cache tests
func filters(topics ...string) []map[string]any {
	bindings := make([]map[string]any, len(topics))
	for i, topic := range topics {
		bindings[i] = map[string]any{"_action": "text", "topic": topic}
	}
	return bindings
}

// Measure retained MQTT message and JSON snapshot budget usage
func cacheUsage(t *testing.T, d *Driver) (int, int) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	bytes := 0
	for _, message := range d.messages {
		message.parsed.mu.Lock()
		bytes += len(message.Topic) + len(message.Text)
		for path, value := range message.parsed.fields {
			bytes += len(path) + len(value.text)
		}
		message.parsed.mu.Unlock()
	}
	if bytes != d.budget.shared().Used() || bytes > messageCacheBytes || len(d.messages) > messageCacheTopics {
		t.Fatalf("cache accounting: actual=%d recorded=%d topics=%d", bytes, d.budget.shared().Used(), len(d.messages))
	}
	return bytes, len(d.messages)
}

// Verify message cache byte plateau and filter churn
func TestMessageCacheBytePlateauAndFilterChurn(t *testing.T) {
	d := NewDriver(nil)
	defer d.Close()
	payload := `{"value":"` + strings.Repeat("x", (64<<10)-12) + `"}`
	d.Demand(filters("pinned", "sensor/0/#"))
	if !d.recordMessage(0, "pinned", payload, true) {
		t.Fatal("initial last-good message rejected")
	}
	pinned := d.messages["pinned"]
	pinned.extracted("value")
	peak, peakTopics := 0, 0
	for epoch := range 5 {
		prefix := fmt.Sprintf("sensor/%d/", epoch)
		d.Demand(filters("pinned", prefix+"#", prefix+"+"))
		for i := range 400 {
			topic := fmt.Sprintf("%s%04d", prefix, i)
			if !d.recordMessage(0, topic, strings.Clone(payload), true) {
				t.Fatal("new active value rejected", topic)
			}
			message := d.messages[topic]
			if got := message.extracted("value"); !got.valid || len(got.text) != (64<<10)-12 {
				t.Fatal("extraction changed at capacity")
			}
			bytes, topics := cacheUsage(t, d)
			peak, peakTopics = max(peak, bytes), max(peakTopics, topics)
		}
		d.mu.Lock()
		latest := d.snapshot(prefix + "#")
		if len(latest) != 1 || latest[prefix+"0399"].Text != payload || !latest[prefix+"0399"].Retained || d.messages["pinned"].Text != payload {
			t.Error("active latest or exact last-good value evicted")
		}
		d.mu.Unlock()
		d.Demand(filters("pinned", prefix+"+"))
		if _, topics := cacheUsage(t, d); topics < 2 {
			t.Fatal("overlapping active filter lost messages")
		}
	}
	d.Demand(filters("pinned"))
	if _, topics := cacheUsage(t, d); topics != 1 {
		t.Fatal("uncovered wildcard topics retained", topics)
	}
	d.Demand(nil)
	if bytes, topics := cacheUsage(t, d); bytes != 0 || topics != 0 {
		t.Fatal("retired filters retained data")
	}
	if d.recordMessage(0, "pinned", "late delivery after unsubscribe", false) {
		t.Fatal("retired topic admitted")
	}
	if value := pinned.extracted("value"); !value.valid || len(value.text) != (64<<10)-12 || pinned.parsed.bytes != 0 {
		t.Fatal("retired sample changed or repopulated extraction cache")
	}
	t.Logf("2,000 max-size payloads, 5 filter generations: peak_bytes=%d peak_topics=%d after_retirement_bytes=0", peak, peakTopics)
}

// Verify message cache count evicts oldest and keeps new last good
func TestMessageCacheCountEvictsOldestAndKeepsNewLastGood(t *testing.T) {
	d := NewDriver(nil)
	defer d.Close()
	d.Demand(filters("#", "pinned"))
	d.recordMessage(0, "pinned", "last-good", true)
	for i := range messageCacheTopics {
		d.recordMessage(0, fmt.Sprintf("topic/%04d", i), "value", false)
	}
	if _, ok := d.messages["topic/0000"]; ok {
		t.Fatal("oldest obsolete wildcard value retained")
	}
	if d.messages["pinned"].Text != "last-good" || d.messages["topic/0999"].Text != "value" {
		t.Fatal("useful active values lost")
	}
	cacheUsage(t, d)
}

// Verify message cache concurrent extraction replacement and retirement
func TestMessageCacheConcurrentExtractionReplacementAndRetirement(t *testing.T) {
	d := NewDriver(nil)
	d.Demand(filters("sensor/#"))
	payload := `{"value":"` + strings.Repeat("x", 32000) + `"}`
	d.recordMessage(0, "sensor/a", payload, false)
	old := d.messages["sensor/a"]
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if value := old.extracted("value"); !value.valid || len(value.text) != 32000 {
					t.Error("published sample changed")
				}
			}
		})
	}
	for range 100 {
		d.recordMessage(0, "sensor/a", payload, false)
	}
	d.Close()
	workers.Wait()
	if bytes, _ := cacheUsage(t, d); bytes != 0 {
		t.Fatal("closed cache still charged")
	}
}
