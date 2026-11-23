package homeassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type haFixture struct {
	mu               sync.Mutex
	url              string
	connections      atomic.Int32
	subscriptions    atomic.Int32
	reads            atomic.Int32
	serviceReads     atomic.Int32
	calls            []map[string]any
	state            string
	sceneState       string
	brightness       *float64
	modes            []string
	uncertain        bool
	deny             bool
	interleave       bool
	unit             string
	omitServices     bool
	denySubscription bool
	disconnectRead   bool
	nullStates       bool
	nullServices     bool
}

// Start a fake Home Assistant WebSocket service for protocol tests
func newHA(t *testing.T) *haFixture {
	t.Helper()
	level := 128.0
	f := &haFixture{state: "on", brightness: &level, modes: []string{"brightness"}, interleave: true, unit: "°C", sceneState: "unknown"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1 << 20)
		f.connections.Add(1)
		_ = wsjson.Write(context.Background(), conn, map[string]any{"type": "auth_required"})
		var auth map[string]any
		if wsjson.Read(context.Background(), conn, &auth) != nil {
			return
		}
		if auth["type"] != "auth" || auth["access_token"] != "fixture-token" {
			_ = wsjson.Write(context.Background(), conn, map[string]any{"type": "auth_invalid", "message": "secret token echo fixture-token"})
			return
		}
		_ = wsjson.Write(context.Background(), conn, map[string]any{"type": "auth_ok"})
		for {
			var request map[string]any
			if wsjson.Read(context.Background(), conn, &request) != nil {
				return
			}
			f.mu.Lock()
			var result any
			closeNow := false
			success := true
			switch request["type"] {
			case "subscribe_events":
				f.subscriptions.Add(1)
				if request["event_type"] != "state_changed" || f.denySubscription {
					success = false
				}
			case "get_states":
				f.reads.Add(1)
				closeNow = f.disconnectRead
				attrs := map[string]any{"friendly_name": "Desk", "supported_color_modes": f.modes}
				if f.brightness != nil {
					attrs["brightness"] = *f.brightness
				}
				result = []any{
					map[string]any{"entity_id": "light.desk", "state": f.state, "attributes": attrs}, map[string]any{"entity_id": "sensor.temperature", "state": "21.5", "attributes": map[string]any{"unit_of_measurement": f.unit}}, map[string]any{"entity_id": "scene.evening", "state": f.sceneState, "attributes": map[string]any{"friendly_name": "Evening"}}, map[string]any{"entity_id": "lock.front", "state": "locked", "attributes": map[string]any{}}, map[string]any{"entity_id": "switch.fan", "state": "off", "attributes": map[string]any{}}}
			case "get_services":
				f.serviceReads.Add(1)
				result = map[string]any{"light": map[string]any{"turn_on": map[string]any{}, "turn_off": map[string]any{}, "toggle": map[string]any{}}, "scene": map[string]any{"turn_on": map[string]any{}}, "switch": map[string]any{"turn_on": map[string]any{}, "turn_off": map[string]any{}, "toggle": map[string]any{}}}
				if f.omitServices {
					result = map[string]any{}
				}
			case "call_service":
				f.calls = append(f.calls, request)
				closeNow = f.uncertain
				success = !f.deny
				if success && request["domain"] == "light" {
					switch request["service"] {
					case "turn_off":
						f.state = "off"
					case "turn_on":
						f.state = "on"
					case "toggle":
						if f.state == "on" {
							f.state = "off"
						} else {
							f.state = "on"
						}
					}
					if b, ok := request["service_data"].(map[string]any)["brightness"].(float64); ok {
						f.brightness = &b
					}
				}
			default:
				success = false
			}
			if request["type"] == "get_states" && f.nullStates || request["type"] == "get_services" && f.nullServices {
				result = nil
			}
			interleave := (f.interleave || f.disconnectRead) && (request["type"] == "get_states" || request["type"] == "subscribe_events" && f.denySubscription)
			if interleave {
				f.interleave = false
			}
			f.mu.Unlock()
			if interleave {
				_ = wsjson.Write(context.Background(), conn, map[string]any{"id": 1, "type": "event", "event": map[string]any{"event_type": "state_changed", "data": map[string]any{"entity_id": "light.desk", "new_state": map[string]any{"state": "on"}}}})
			}
			if closeNow {
				return
			}
			_ = wsjson.Write(context.Background(), conn, map[string]any{"id": request["id"], "type": "result", "success": success, "result": result, "error": map[string]any{"message": "fixture-token must not leak"}})
		}
	}))
	t.Cleanup(server.Close)
	f.url = server.URL
	return f
}

// Build Home Assistant connection settings for the test service
func (f *haFixture) settings() map[string]any {
	return map[string]any{"url": f.url, "token": "fixture-token", "entity": "light.desk", "_action": "brightness"}
}

// Verify authenticated subscription state discovery and dial
func TestAuthenticatedSubscriptionStateDiscoveryAndDial(t *testing.T) {
	f := newHA(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	d.Demand([]map[string]any{s})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(first, "brightness", s) != "50%" {
		t.Fatal(first)
	}
	if !d.Changed() {
		t.Fatal("state event did not wake plugin")
	}
	if _, err = d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	if f.reads.Load() != 2 {
		t.Fatal("interleaved event did not invalidate snapshot", f.reads.Load())
	}
	choices, err := d.Discover(ctx, s)
	if err != nil || len(choices) != 1 || choices[0].ID != "light.desk" {
		t.Fatal(choices, err)
	}
	for _, a := range []string{"on", "off", "toggle"} {
		if _, err = d.Command(ctx, a, s, 0); err != nil {
			t.Fatal(a, err)
		}
	}
	if _, err = d.Command(ctx, "brightness", s, 1000); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	brightness := *f.brightness
	f.mu.Unlock()
	if brightness != 255 {
		t.Fatal("brightness maximum", brightness)
	}
	if _, err = d.Command(ctx, "brightness", s, -1000); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	brightness = *f.brightness
	f.mu.Unlock()
	if brightness != 0 {
		t.Fatal("brightness minimum", brightness)
	}
	s["entity"] = "scene.evening"
	s["_action"] = "scene"
	choices, err = d.Discover(ctx, s)
	if err != nil || len(choices) != 1 || choices[0].ID != "scene.evening" {
		t.Fatal(choices, err)
	}
	if _, err = d.Command(ctx, "scene", s, 0); err != nil {
		t.Fatal(err)
	}
	s["entity"] = "sensor.temperature"
	sample, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(sample, "state", s) != "21.5 °C" {
		t.Fatal(sample)
	}
	if f.connections.Load() != 1 || f.subscriptions.Load() != 1 {
		t.Fatal("connection or subscription not shared", f.connections.Load(), f.subscriptions.Load())
	}
}

// Verify uncertain delivery is not replayed and reconnect refreshes
func TestUncertainDeliveryIsNotReplayedAndReconnectRefreshes(t *testing.T) {
	f := newHA(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.uncertain = true
	f.mu.Unlock()
	if _, err = d.Command(ctx, "off", s, 0); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.uncertain = false
	f.mu.Unlock()
	next, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(next, "state", s) != "off" || d.Display(first, "state", s) != "on" {
		t.Fatal("stale or mutated snapshot", first, next)
	}
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != 1 || f.connections.Load() != 2 || f.subscriptions.Load() != 2 {
		t.Fatal("write replay or missing fresh subscription", calls, f.connections.Load(), f.subscriptions.Load())
	}
	d.Close()
	s["token"] = "wrong-token"
	if _, err = d.Read(ctx, s); err == nil || strings.Contains(err.Error(), "wrong-token") || strings.Contains(err.Error(), "fixture-token") {
		t.Fatal("authentication error leaks token", err)
	}
}

// Verify capabilities availability and server rejection
func TestCapabilitiesAvailabilityAndServerRejection(t *testing.T) {
	f := newHA(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	for _, state := range []string{"unknown", "unavailable"} {
		f.mu.Lock()
		f.state = state
		f.mu.Unlock()
		d.notify()
		value, err := d.Read(context.Background(), s)
		if err != nil || d.Display(value, "state", s) != state {
			t.Fatal(value, err)
		}
		if _, err = d.Command(context.Background(), "on", s, 0); err == nil {
			t.Fatal("command accepted for", state)
		}
	}
	f.mu.Lock()
	f.state = "on"
	f.modes = []string{"onoff"}
	f.mu.Unlock()
	if _, err := d.Command(context.Background(), "brightness", s, 1); err == nil {
		t.Fatal("non-dimmable light accepted")
	}
	f.mu.Lock()
	f.modes = []string{"brightness"}
	f.brightness = nil
	f.mu.Unlock()
	if _, err := d.Command(context.Background(), "brightness", s, 1); err == nil {
		t.Fatal("absent brightness coerced to zero")
	}
	s["entity"] = "lock.front"
	if _, err := d.Command(context.Background(), "on", s, 0); err == nil {
		t.Fatal("unsupported domain accepted")
	}
	s["entity"] = "sensor.temperature"
	if _, err := d.Command(context.Background(), "scene", s, 0); err == nil {
		t.Fatal("non-scene accepted")
	}
	s["entity"] = "light.missing"
	if _, err := d.Command(context.Background(), "on", s, 0); err == nil {
		t.Fatal("missing entity accepted")
	}
	s["entity"] = "light.desk"
	f.mu.Lock()
	calls := len(f.calls)
	f.omitServices = true
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("invalid entity triggered a write")
	}
	if _, err := d.Command(context.Background(), "on", s, 0); err == nil {
		t.Fatal("unsupported service accepted")
	}
	d.Close()
	f.mu.Lock()
	f.omitServices = false
	f.deny = true
	f.mu.Unlock()
	if _, err := d.Command(context.Background(), "on", s, 0); err == nil || !strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "uncertain") || strings.Contains(err.Error(), "fixture-token") {
		t.Fatal("rejection mishandled", err)
	}
}

// Verify event demand filter refresh and URL validation
func TestEventDemandFilterRefreshAndURLValidation(t *testing.T) {
	f := newHA(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	d.Demand([]map[string]any{s})
	if _, err := d.Read(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	d.Changed()
	_, err := d.routeNotifications(json.RawMessage(`{"type":"event","id":1,"event":{"event_type":"state_changed","data":{"entity_id":"light.other"}}}`), nil)
	if err != nil || d.Changed() {
		t.Fatal("unrelated event invalidated state", err)
	}
	if _, err = d.Command(context.Background(), "refresh", s, 0); err != nil || !d.Changed() {
		t.Fatal("refresh did not invalidate", err)
	}
	for _, raw := range []string{"http://user:pass@localhost", "file:///tmp/ha", "https://localhost?token=secret", "https://localhost/#secret"} {
		if _, err = endpoint(raw); err == nil {
			t.Fatal("invalid URL accepted", raw)
		}
	}
	if address, err := endpoint("https://example.test/prefix/"); err != nil || address != "wss://example.test/prefix/api/websocket" {
		t.Fatal(address, err)
	}
}

// Verify subscription failures preserve failure backoff
func TestSubscriptionFailuresPreserveFailureBackoff(t *testing.T) {
	f := newHA(t)
	f.denySubscription = true
	wake := make(chan struct{}, 1)
	d := NewDriver(wake)
	defer d.Close()
	s := f.settings()
	for attempt := range 3 {
		f.mu.Lock()
		f.interleave = true
		f.mu.Unlock()
		if _, err := d.Read(context.Background(), s); err == nil {
			t.Fatal("denied subscription accepted")
		}
		if d.Changed() {
			t.Fatal("denied subscription invalidated observations and bypassed retry backoff", attempt)
		}
		select {
		case <-wake:
			t.Fatal("failed subscription woke connector")
		default:
		}
	}
	f.mu.Lock()
	f.denySubscription = false
	f.mu.Unlock()
	if _, err := d.Read(context.Background(), s); err != nil {
		t.Fatal("recovery failed", err)
	}
	d.Changed()
	d.Close()
	if !d.Changed() {
		t.Fatal("initialized connection closure did not invalidate observations")
	}
}

// Verify disconnect before first snapshot preserves failure backoff
func TestDisconnectBeforeFirstSnapshotPreservesFailureBackoff(t *testing.T) {
	f := newHA(t)
	f.disconnectRead = true
	wake := make(chan struct{}, 1)
	d := NewDriver(wake)
	defer d.Close()
	s := f.settings()
	for attempt := range 4 {
		if _, err := d.Read(context.Background(), s); err == nil {
			t.Fatal("disconnected snapshot accepted")
		}
		if d.Changed() {
			t.Fatal("first-read disconnect or event bypassed failure backoff", attempt)
		}
		select {
		case <-wake:
			t.Fatal("failed first snapshot woke connector")
		default:
		}
	}
	if f.connections.Load() != 4 || f.subscriptions.Load() != 4 {
		t.Fatal("fixture did not exercise authenticated reconnects", f.connections.Load(), f.subscriptions.Load())
	}
	f.mu.Lock()
	f.disconnectRead = false
	f.mu.Unlock()
	if _, err := d.Read(context.Background(), s); err != nil {
		t.Fatal("recovery failed", err)
	}
	d.Changed()
	d.Close()
	if !d.Changed() {
		t.Fatal("established snapshot did not enable close notification")
	}
}

// Verify service registry refresh and expiry recover without reconnect
func TestServiceRegistryRefreshAndExpiryRecoverWithoutReconnect(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "refresh"}[refresh], func(t *testing.T) {
			f := newHA(t)
			f.mu.Lock()
			f.omitServices = true
			f.mu.Unlock()
			d := NewDriver(nil)
			defer d.Close()
			s := f.settings()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := d.Command(ctx, "on", s, 0); err == nil {
				t.Fatal("missing service accepted")
			}
			f.mu.Lock()
			f.omitServices = false
			f.mu.Unlock()
			if refresh {
				if _, err := d.Command(ctx, "refresh", s, 0); err != nil {
					t.Fatal(err)
				}
			} else {
				d.mu.Lock()
				d.servicesAt = time.Now().Add(-time.Minute)
				d.mu.Unlock()
			}
			if _, err := d.Command(ctx, "on", s, 0); err != nil {
				t.Fatal("service did not recover", err)
			}
			if f.serviceReads.Load() != 2 || f.connections.Load() != 1 {
				t.Fatal("registry was not refreshed on the existing session", f.serviceReads.Load(), f.connections.Load())
			}
			if _, err := d.Command(ctx, "off", s, 0); err != nil {
				t.Fatal(err)
			}
			if f.serviceReads.Load() != 2 {
				t.Fatal("fresh registry not shared")
			}
		})
	}
}

// Verify null snapshots and service registries remain unavailable
func TestNullSnapshotsAndServiceRegistriesRemainUnavailable(t *testing.T) {
	f := newHA(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sample, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.nullStates = true
	f.mu.Unlock()
	d.notify()
	if _, err = d.Read(ctx, s); err == nil {
		t.Fatal("null entity snapshot treated as a fresh empty registry")
	}
	if d.Display(sample, "state", s) != "on" {
		t.Fatal("previous good snapshot was mutated")
	}
	f.mu.Lock()
	f.nullStates = false
	f.nullServices = true
	f.mu.Unlock()
	if _, err = d.Command(ctx, "on", s, 0); err == nil || !strings.Contains(err.Error(), "invalid service registry") {
		t.Fatal("null service registry accepted", err)
	}
	f.mu.Lock()
	calls := len(f.calls)
	f.nullServices = false
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("malformed registry allowed a write", calls)
	}
	if _, err = d.Command(ctx, "on", s, 0); err != nil {
		t.Fatal("malformed registry was cached", err)
	}
}

func TestSelectedStateChangePreservesDeliveredSnapshot(t *testing.T) {
	d := NewDriver(make(chan struct{}, 1))
	id := "light.selected"
	d.Demand([]map[string]any{{"entity": id}})
	original := Sample{Entities: map[string]entity{id: {ID: id, State: "off"}}}
	d.snapshot.Store(&original, d.revision, time.Now())
	push := func(id string, state any) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"type": "event", "event": map[string]any{"event_type": "state_changed", "data": map[string]any{"entity_id": id, "new_state": state}}})
		if _, err := d.routeNotifications(data, nil); err != nil {
			t.Fatal(err)
		}
	}
	push("light.other", map[string]any{"entity_id": "light.other", "state": "on"})
	if d.snapshot.Value != &original {
		t.Fatal("unbound entity replaced the snapshot")
	}
	push(id, map[string]any{"entity_id": id, "state": "on"})
	if original.Entities[id].State != "off" || d.snapshot.Value.Entities[id].State != "on" || d.snapshot.Revision != d.revision {
		t.Fatal("update lost or mutated the delivered sample")
	}
	push(id, nil)
	if len(d.snapshot.Value.Entities) != 0 || d.snapshot.Revision != d.revision {
		t.Fatal("removed entity still retained")
	}
	push(id, map[string]any{})
	if d.snapshot.Revision == d.revision {
		t.Fatal("incomplete event should require an authoritative read")
	}
}
