package mqtt

import (
	"encoding/json"
	cache "github.com/StreamDockManager/Plugin-SDK/Go/cache"
	"strings"
	"sync"
	"time"
)

const messageCacheBytes = 8 << 20
const messageCacheTopics = 1000

// The budget belongs to this driver's cached strings. Published samples may
// outlive cache eviction, but cannot add extracted fields after retirement
// Extraction never takes d.mu: the lock order is d.mu, parsed.mu, budget.mu
type messageBudget struct {
	once  sync.Once
	value *cache.Budget
}

// Create or return the shared retained-message byte budget
func (b *messageBudget) shared() *cache.Budget {
	b.once.Do(func() { b.value, _ = cache.NewBudget(messageCacheBytes) })
	return b.value
}

// Reserve bytes before retaining an MQTT message snapshot
func (b *messageBudget) reserve(size int) bool { return b.shared().Reserve(size) }

// Return bytes previously charged to the shared message budget
func (b *messageBudget) release(size int) {
	if err := b.shared().Release(size); err != nil {
		panic(err)
	}
}

type messageJSON struct {
	mu           sync.Mutex
	invalid      bool
	bytes        int
	fields       map[string]extractedValue
	budget       *messageBudget
	payloadBytes int
	retired      bool
}

// Release the retained JSON snapshot budget
func (p *messageJSON) retire() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.budget != nil {
		p.budget.release(p.payloadBytes + p.bytes)
		p.budget = nil
	}
	p.retired = true
	p.fields = nil
	p.bytes = 0
}

type extractedValue struct {
	text  string
	valid bool
}

// Read a cached JSON path from an immutable message snapshot
func (message Message) extracted(path string) extractedValue {
	parsed := message.parsed
	if parsed == nil {
		parsed = &messageJSON{}
	}
	parsed.mu.Lock()
	defer parsed.mu.Unlock()
	if value, ok := parsed.fields[path]; ok {
		return value
	}
	if parsed.invalid {
		return extractedValue{text: "Malformed MQTT JSON"}
	}
	// Retain bounded extracted values, not an arbitrarily expanded JSON tree
	// Each selected path parses once while cached; concurrent readers share it
	var item any
	if json.Unmarshal([]byte(message.Text), &item) != nil {
		parsed.invalid = true
		return extractedValue{text: "Malformed MQTT JSON"}
	}
	text := "JSON field unavailable"
	found := true
	for key := range strings.SplitSeq(path, ".") {
		object, ok := item.(map[string]any)
		if !ok {
			found = false
			break
		}
		item, ok = object[key]
		if !ok {
			found = false
			break
		}
	}
	if found {
		if value, ok := item.(string); ok {
			text = value
		} else {
			data, _ := json.Marshal(item)
			text = string(data)
		}
	}
	size := len(path) + len(text)
	if !parsed.retired && len(parsed.fields) < 64 && parsed.bytes+size <= 64<<10 && (parsed.budget == nil || parsed.budget.reserve(size)) {
		if parsed.fields == nil {
			parsed.fields = map[string]extractedValue{}
		}
		parsed.fields[strings.Clone(path)] = extractedValue{text: text, valid: found}
		parsed.bytes += size
	}
	return extractedValue{text: text, valid: found}
}

// Admit a bounded MQTT payload only for the current connection generation
func (d *Driver) receiveMessage(generation uint64, topic string, payload []byte, retained bool) bool {
	if len(payload) > 64<<10 {
		return d.recordReceipt(generation, topic, "", retained, "payload exceeds 64 KiB", len(payload))
	}
	return d.recordMessage(generation, topic, string(payload), retained)
}

// Record a decoded MQTT message for its topic
func (d *Driver) recordMessage(generation uint64, topic, text string, retained bool) bool {
	if len(text) > 64<<10 {
		return d.recordReceipt(generation, topic, "", retained, "payload exceeds 64 KiB", len(text))
	}
	return d.recordReceipt(generation, topic, text, retained, "", len(text))
}

// Store message receipt metadata and explicit rejection information
func (d *Driver) recordReceipt(generation uint64, topic, text string, retained bool, rejection string, payloadBytes int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if generation != d.generation || len(text) > 64<<10 || len(topic) > 65535 || !d.covered(topic) {
		return false
	}
	d.removeMessage(topic)
	d.messageOrder++
	message := Message{Topic: topic, Text: text, Retained: retained, At: time.Now(), order: d.messageOrder, Rejection: rejection, PayloadBytes: payloadBytes}
	d.messages[topic] = message
	size := len(topic) + len(text)
	for len(d.messages) > messageCacheTopics || !d.budget.reserve(size) {
		d.removeMessage(d.evictionTopic(topic))
	}
	message.parsed = &messageJSON{budget: d.budget, payloadBytes: size}
	d.messages[topic] = message
	d.invalidate(topic)
	return true
}

// Invalidate derived snapshots affected by an MQTT topic update
func (d *Driver) invalidate(topic string) {
	for filter := range d.snapshots {
		if matches(filter, topic) {
			delete(d.snapshots, filter)
		}
	}
}

// All helpers below require d.mu. Desired filters include subscriptions still
// awaiting acknowledgement so immediate retained publications are admitted
func (d *Driver) activeFilters() map[string]byte {
	if d.demandKnown {
		return d.desired
	}
	return d.receiving
}

// Check whether an MQTT topic is needed by any active filter
func (d *Driver) covered(topic string) bool {
	for filter, qos := range d.activeFilters() {
		if qos <= 2 && matches(filter, topic) {
			return true
		}
	}
	return false
}

// Remove a topic snapshot and release its retained-byte charges
func (d *Driver) removeMessage(topic string) {
	if message, ok := d.messages[topic]; ok {
		message.parsed.retire()
		delete(d.messages, topic)
		d.invalidate(topic)
	}
}

// Discard MQTT snapshots no longer required by active bindings
func (d *Driver) pruneMessages() {
	for topic := range d.messages {
		if !d.covered(topic) {
			d.removeMessage(topic)
		}
	}
}

// Compare message receipt ordering without relying on payload contents
func newer(a, b Message) bool {
	return a.order > b.order || (a.order == b.order && a.Topic < b.Topic)
}

// Choose the newest retained message matching a topic filter
func (d *Driver) selected(filter string) (Message, bool) {
	selected, found := d.messages[filter]
	if !found {
		for _, candidate := range d.messages {
			if matches(filter, candidate.Topic) && (!found || newer(candidate, selected)) {
				selected, found = candidate, true
			}
		}
	}
	return selected, found
}

// Prefer obsolete wildcard history over each active filter's latest value
// If those useful values alone exceed the budget, evict oldest receipt first
// Receipt order and topic break ties, independent of Go map iteration order
func (d *Driver) evictionTopic(incoming string) string {
	useful := map[string]bool{}
	for filter, qos := range d.activeFilters() {
		if qos <= 2 {
			if message, ok := d.selected(filter); ok {
				useful[message.Topic] = true
			}
		}
	}
	var victim Message
	found := false
	for topic, message := range d.messages {
		if topic == incoming {
			continue
		}
		if !found || (useful[victim.Topic] && !useful[topic]) || (useful[victim.Topic] == useful[topic] && newer(victim, message)) {
			victim, found = message, true
		}
	}
	return victim.Topic
}

// Must be called with d.mu held. Published maps/messages are immutable; only
// filters affected by a new topic lose their cached snapshot
func (d *Driver) snapshot(filter string) map[string]Message {
	if snapshot, ok := d.snapshots[filter]; ok {
		return snapshot
	}
	snapshot := map[string]Message{}
	selected, found := d.selected(filter)
	if found {
		snapshot[selected.Topic] = selected
	}
	if d.snapshots == nil {
		d.snapshots = map[string]map[string]Message{}
	}
	if len(d.snapshots) >= 256 {
		clear(d.snapshots)
	}
	d.snapshots[filter] = snapshot
	return snapshot
}
