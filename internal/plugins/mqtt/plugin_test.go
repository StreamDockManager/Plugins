package mqtt

import (
	"context"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/connectors/testutil"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Verify shared subscriptions retained messages acknowledgements and no replay
func TestSharedSubscriptionsRetainedMessagesAcknowledgementsAndNoReplay(t *testing.T) {
	var connections, subscribes, publishes atomic.Int32
	var unsubscribes atomic.Int32
	address := testutil.Serve(t, func(conn net.Conn) {
		connections.Add(1)
		for {
			packet, err := packets.ReadPacket(conn)
			if err != nil {
				return
			}
			switch p := packet.(type) {
			case *packets.ConnectPacket:
				ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
				if p.Username != "fixture" || string(p.Password) != "fixture-password" {
					ack.ReturnCode = 4
				}
				_ = ack.Write(conn)
			case *packets.SubscribePacket:
				subscribes.Add(1)
				ack := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
				ack.MessageID = p.MessageID
				ack.ReturnCodes = p.Qoss
				_ = ack.Write(conn)
				message := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
				message.TopicName = "sensor/temperature"
				message.Payload = []byte(`{"temperature":21.5}`)
				message.Retain = true
				_ = message.Write(conn)
			case *packets.PublishPacket:
				publishes.Add(1)
				if p.TopicName == "uncertain" {
					return
				}
				switch p.Qos {
				case 1:
					ack := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
					ack.MessageID = p.MessageID
					_ = ack.Write(conn)
				case 2:
					ack := packets.NewControlPacket(packets.Pubrec).(*packets.PubrecPacket)
					ack.MessageID = p.MessageID
					_ = ack.Write(conn)
				}
			case *packets.PubrelPacket:
				ack := packets.NewControlPacket(packets.Pubcomp).(*packets.PubcompPacket)
				ack.MessageID = p.MessageID
				_ = ack.Write(conn)
			case *packets.UnsubscribePacket:
				unsubscribes.Add(1)
				ack := packets.NewControlPacket(packets.Unsuback).(*packets.UnsubackPacket)
				ack.MessageID = p.MessageID
				_ = ack.Write(conn)
			case *packets.PingreqPacket:
				_ = packets.NewControlPacket(packets.Pingresp).Write(conn)
			case *packets.DisconnectPacket:
				return
			}
		}
	})
	wake := make(chan struct{}, 1)
	d := NewDriver(wake)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s := map[string]any{"broker": "tcp://" + address, "username": "fixture", "password": "fixture-password", "topic": "sensor/+", "qos": "1", "json_path": "temperature", "_action": "text"}
	if _, err := d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return len(d.messages) == 1 })
	value, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if text := d.Display(value, "text", s); !strings.Contains(text, "21.5") || !strings.Contains(text, "retained") {
		t.Fatal(text)
	}
	if connections.Load() != 1 || subscribes.Load() != 1 {
		t.Fatal("connection/subscription not shared", connections.Load(), subscribes.Load())
	}
	d.Demand([]map[string]any{})
	if _, err = d.Read(ctx, s); err != nil || unsubscribes.Load() != 1 {
		t.Fatal("unused subscription retained", err, unsubscribes.Load())
	}
	d.mu.Lock()
	if len(d.messages) != 0 || len(d.snapshots[s["topic"].(string)]) != 0 {
		t.Error("unsubscribed topic payload retained")
	}
	d.mu.Unlock()
	s["topic"], s["payload"], s["_action"] = "control", "fixture", "publish"
	for _, q := range []string{"0", "1", "2"} {
		s["qos"] = q
		outcome, err := d.Command(ctx, "publish", s, 0)
		if err != nil {
			t.Fatal(err)
		}
		if q == "0" && !strings.Contains(outcome.Message, "no broker acknowledgement") {
			t.Fatal(outcome)
		}
	}
	testutil.Eventually(t, func() bool { return publishes.Load() == 3 })
	s["topic"], s["qos"] = "uncertain", "1"
	if _, err = d.Command(ctx, "publish", s, 0); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal(err)
	}
	s["_action"] = "status"
	if _, err = d.Read(ctx, s); err != nil {
		t.Fatal("reconnect", err)
	}
	if publishes.Load() != 4 {
		t.Fatal("uncertain command replayed", publishes.Load())
	}
	d.Close()
	s["password"] = "wrong"
	if _, err = d.Read(ctx, s); err == nil {
		t.Fatal("authentication failure accepted")
	}
}

// Verify denied subscription and cancelled connection
func TestDeniedSubscriptionAndCancelledConnection(t *testing.T) {
	address := testutil.Serve(t, func(conn net.Conn) {
		for {
			packet, err := packets.ReadPacket(conn)
			if err != nil {
				return
			}
			switch p := packet.(type) {
			case *packets.ConnectPacket:
				ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
				_ = ack.Write(conn)
			case *packets.SubscribePacket:
				ack := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
				ack.MessageID = p.MessageID
				ack.ReturnCodes = []byte{0x80}
				_ = ack.Write(conn)
			case *packets.DisconnectPacket:
				return
			}
		}
	})
	d := NewDriver(nil)
	defer d.Close()
	s := map[string]any{"broker": "tcp://" + address, "topic": "denied", "_action": "text"}
	if _, err := d.Read(context.Background(), s); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatal(err)
	}
	stalled := testutil.Serve(t, func(conn net.Conn) { _, _ = packets.ReadPacket(conn); var b [1]byte; _, _ = conn.Read(b[:]) })
	s["broker"] = "tcp://" + stalled
	s["_action"] = "status"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := d.Read(ctx, s); err == nil || time.Since(start) > time.Second {
		t.Fatal("connection cancellation failed", err)
	}
}

// Verify packet limit and topic matching
func TestPacketLimitAndTopicMatching(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = b.Write([]byte{0x30, 0xff, 0xff, 0xff, 0x7f}) }()
	guarded := &packetConn{Conn: a}
	var out [8]byte
	if _, err := guarded.Read(out[:]); err == nil {
		t.Fatal("oversized packet accepted")
	}
	for _, tc := range []struct {
		filter, topic string
		want          bool
	}{{"sensor/+", "sensor/t", true}, {"sensor/#", "sensor/t/x", true}, {"sensor/+", "sensor/t/x", false}, {"#", "$SYS/status", false}, {"$SYS/#", "$SYS/status", true}} {
		if matches(tc.filter, tc.topic) != tc.want {
			t.Fatal(fmt.Sprint(tc))
		}
	}
	d := NewDriver(nil)
	sample := Sample{Connected: true, Messages: map[string]Message{"x": {Topic: "x", Text: `{"ok":false}`}}}
	if text := d.Display(sample, "text", map[string]any{"topic": "x", "json_path": "absent"}); text != "JSON field unavailable" {
		t.Fatal(text)
	}
}

// Verify message snapshots invalidate only matching topics and cache extraction
func TestMessageSnapshotsInvalidateOnlyMatchingTopicsAndCacheExtraction(t *testing.T) {
	d := NewDriver(nil)
	d.Demand([]map[string]any{{"_action": "text", "topic": "#"}})
	d.recordMessage(0, "sensor/a", `{"nested":{"value":1}}`, true)
	d.recordMessage(0, "other/a", `{"value":2}`, false)
	d.mu.Lock()
	first := d.snapshot("sensor/#")
	unrelated := d.snapshot("other/#")
	d.mu.Unlock()
	firstMessage := first["sensor/a"]
	for range 10 {
		if text := firstMessage.extracted("nested.value").text; text != "1" {
			t.Fatal(text)
		}
	}
	if len(firstMessage.parsed.fields) != 1 {
		t.Fatal("extraction was not cached")
	}
	d.recordMessage(0, "sensor/a", `{"nested":{"value":3}}`, false)
	d.mu.Lock()
	next := d.snapshot("sensor/#")
	same := d.snapshot("other/#")
	d.mu.Unlock()
	if first["sensor/a"].Text != `{"nested":{"value":1}}` || next["sensor/a"].extracted("nested.value").text != "3" {
		t.Fatal("prior snapshot mutated or new value missing")
	}
	if reflect.ValueOf(same).Pointer() != reflect.ValueOf(unrelated).Pointer() {
		t.Fatal("unrelated topic discarded snapshot")
	}
	if d.recordMessage(99, "sensor/a", "stale", false) {
		t.Fatal("retired connection published message")
	}
	d.Close()
	if firstMessage.extracted("nested.value").text != "1" {
		t.Fatal("closing connection mutated retained sample")
	}
}

// Verify concurrent message extraction is immutable
func TestConcurrentMessageExtractionIsImmutable(t *testing.T) {
	d := NewDriver(nil)
	d.Demand([]map[string]any{{"_action": "text", "topic": "sensor/#"}})
	d.recordMessage(0, "sensor/a", `{"nested":{"value":"JSON field unavailable"}}`, false)
	d.mu.Lock()
	snapshot := d.snapshot("sensor/#")
	d.mu.Unlock()
	sample := Sample{Connected: true, Messages: snapshot}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			for range 50 {
				if got := d.Display(sample, "text", map[string]any{"topic": "sensor/#", "json_path": "nested.value"}); !strings.HasPrefix(got, "sensor/a · received ") || !strings.HasSuffix(got, "\nJSON field unavailable") {
					t.Error(got)
				}
			}
		})
	}
	workers.Wait()
	if snapshot["sensor/a"].Text != `{"nested":{"value":"JSON field unavailable"}}` {
		t.Fatal("cached extraction mutated message")
	}
}

// Verify extraction cache has aggregate byte bound
func TestExtractionCacheHasAggregateByteBound(t *testing.T) {
	d := NewDriver(nil)
	d.Demand([]map[string]any{{"_action": "text", "topic": "sensor/a"}})
	payload := `{"value":"` + strings.Repeat("x", 40000) + `"}`
	d.recordMessage(0, "sensor/a", payload, false)
	d.mu.Lock()
	message := d.snapshot("sensor/a")["sensor/a"]
	d.mu.Unlock()
	if value := message.extracted("value"); !value.valid || len(value.text) != 40000 {
		t.Fatal("valid large field lost")
	}
	for i := range 100 {
		message.extracted(strings.Repeat("p", 2000) + fmt.Sprint(i))
	}
	if message.parsed.bytes > 64<<10 || len(message.parsed.fields) > 64 {
		t.Fatal("extraction cache exceeded bounds", message.parsed.bytes, len(message.parsed.fields))
	}
}
