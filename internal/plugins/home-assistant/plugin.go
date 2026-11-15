package homeassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"maps"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go/connectedws"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var Icons = map[string]string{"on": "play", "off": "stop", "toggle": "cycle", "scene": "list", "brightness": "sun", "state": "list", "refresh": "cycle"}
var Definition, visuals = c.Define("home-assistant", "Home Assistant", Icons, "on", "off", "toggle", "scene", "brightness", "state", "refresh")

// Declare the Home Assistant global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Text("url", "Server URL", "http://homeassistant.local:8123"), metadata.Sensitive("token", "Long-lived access token")}
}

// Declare settings supported by the selected Home Assistant action
func ActionFields(a string) []settings.Field {
	if a == "refresh" {
		return nil
	}
	fields := []settings.Field{metadata.Device("entity", "Entity")}
	if a == "brightness" {
		fields = append(fields, metadata.Numeric("step", "Brightness percent per dial tick", 5, 0.1, 25))
	}
	return fields
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: time.Minute, Dial: true, DialActions: engine.AdditiveActions(Definition), New: func(wake chan<- struct{}) c.Driver { return NewDriver(wake) }})

type entity struct {
	ID         string `json:"entity_id"`
	State      string `json:"state"`
	Attributes struct {
		Name       string   `json:"friendly_name"`
		Unit       string   `json:"unit_of_measurement"`
		Brightness *float64 `json:"brightness"`
		ColorModes []string `json:"supported_color_modes"`
	} `json:"attributes"`
}
type Sample struct{ Entities map[string]entity }
type response struct {
	ID      uint64          `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   struct {
		Type string `json:"event_type"`
		Data struct {
			ID string `json:"entity_id"`
		} `json:"data"`
	} `json:"event"`
}
type Driver struct {
	client      connectedws.Client
	mu          sync.Mutex
	wake        chan<- struct{}
	dirty       atomic.Bool
	seq         atomic.Uint64
	revision    uint64
	wanted      map[string]bool
	demandKnown bool
	snapshot    engine.Snapshot[Sample]
	session     *connectedws.Session
	observed    *atomic.Bool
	services    map[string]map[string]json.RawMessage
	servicesAt  time.Time
}

// Initialize the Home Assistant service driver and its wake signaling
func NewDriver(wake chan<- struct{}) *Driver { return &Driver{wake: wake, wanted: map[string]bool{}} }

// Choose the shared Home Assistant resource identity for these settings
func (*Driver) Key(map[string]any) string { return "connection" }

// Consume the Home Assistant dirty flag after a service notification
func (d *Driver) Changed() bool { return d.dirty.Swap(false) }

// Wake the Home Assistant owner loop after a state change
func (d *Driver) notify() { d.eventChanged(nil) }

// Record a ready Home Assistant notification and wake the owner loop
func (d *Driver) eventChanged(ready func() bool) {
	d.mu.Lock()
	d.revision++
	d.mu.Unlock()
	if ready != nil && !ready() {
		return
	}
	engine.SignalObservation(&d.dirty, d.wake)
}

// Retain Home Assistant resources required by the current bindings
func (d *Driver) Demand(bindings []map[string]any) {
	wanted := map[string]bool{}
	for _, s := range bindings {
		if id := c.String(s, "entity", ""); id != "" && len(wanted) < 10001 {
			wanted[id] = true
		}
	}
	d.mu.Lock()
	d.wanted = wanted
	d.demandKnown = true
	d.mu.Unlock()
	d.notify()
}

// Release the Home Assistant driver resources during shutdown
func (d *Driver) Close() {
	d.client.Close()
	d.mu.Lock()
	d.session = nil
	d.observed = nil
	d.snapshot.Value = nil
	d.services = nil
	d.servicesAt = time.Time{}
	d.mu.Unlock()
}

// Convert the configured Home Assistant URL to its WebSocket endpoint
func endpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid Home Assistant server URL")
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("Home Assistant URL must use http:// or https://")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/api/websocket") {
		u.Path += "/api/websocket"
	}
	u.RawPath = ""
	return u.String(), nil
}

// Complete the Home Assistant access-token handshake
func authenticate(ctx context.Context, conn *websocket.Conn, token string) error {
	var hello struct {
		Type string `json:"type"`
	}
	if wsjson.Read(ctx, conn, &hello) != nil || hello.Type != "auth_required" {
		return fmt.Errorf("Home Assistant authentication greeting was not received")
	}
	if wsjson.Write(ctx, conn, map[string]any{"type": "auth", "access_token": token}) != nil || wsjson.Read(ctx, conn, &hello) != nil || hello.Type != "auth_ok" {
		return fmt.Errorf("Home Assistant authentication failed; check the access token")
	}
	return nil
}

// Separate correlated service replies from state-change notifications
func (d *Driver) routeNotifications(raw json.RawMessage, notify func() bool) (string, error) {
	var r response
	if json.Unmarshal(raw, &r) != nil {
		return "", fmt.Errorf("invalid Home Assistant message")
	}
	switch r.Type {
	case "result":
		if r.ID == 0 {
			return "", fmt.Errorf("missing Home Assistant request ID")
		}
		return strconv.FormatUint(r.ID, 10), nil
	case "event":
		if r.Event.Type == "state_changed" {
			d.mu.Lock()
			relevant := !d.demandKnown || d.wanted[r.Event.Data.ID]
			d.mu.Unlock()
			if relevant {
				d.eventChanged(notify)
			}
		}
		return "", nil
	default:
		return "", fmt.Errorf("unexpected Home Assistant message")
	}
}

// Reuse or establish the configured Home Assistant service connection
func (d *Driver) connection(ctx context.Context, s map[string]any) (*connectedws.Session, error) {
	token, err := c.Secret(s, "token")
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("set a Home Assistant long-lived access token")
	}
	address, err := endpoint(c.String(s, "url", "http://homeassistant.local:8123"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(address + "\x00" + token))
	// Until a snapshot succeeds, events only advance revision and closure stays
	// silent. Authentication followed by repeated read failures must preserve the
	// connector's failure backoff instead of scheduling immediate reconnects
	observed := &atomic.Bool{}
	session, fresh, err := d.client.Connect(ctx, hex.EncodeToString(sum[:]), address,
		func(ctx context.Context, conn *websocket.Conn) error { return authenticate(ctx, conn, token) },
		// Gate event notifications on completed session observation setup
		func(raw json.RawMessage) (string, error) { return d.routeNotifications(raw, observed.Load) },
		func() {
			if observed.Load() {
				d.notify()
			}
		})
	if err != nil {
		return nil, err
	}
	if !fresh {
		return session, nil
	}
	// Subscribe before the authoritative snapshot: any intervening event increments
	// revision, preventing that snapshot from being treated as current on the next read
	if err = d.request(ctx, session, "subscribe_events", map[string]any{"event_type": "state_changed"}, nil); err != nil {
		d.Close()
		return nil, err
	}
	d.mu.Lock()
	d.snapshot.Value = nil
	d.services = nil
	d.servicesAt = time.Time{}
	d.session = session
	d.observed = observed
	d.mu.Unlock()
	return session, nil
}

// Send a bounded Home Assistant service request and decode its response
func (d *Driver) request(ctx context.Context, session *connectedws.Session, kind string, data map[string]any, out any) error {
	id := d.seq.Add(1)
	if id > 1<<53-1 {
		return fmt.Errorf("Home Assistant request ID limit reached; restart the plugin")
	}
	payload := map[string]any{"id": id, "type": kind}
	maps.Copy(payload, data)
	raw, err := session.Request(ctx, strconv.FormatUint(id, 10), payload)
	if err != nil {
		return err
	}
	var r response
	if json.Unmarshal(raw, &r) != nil || r.Type != "result" || r.ID != id {
		return fmt.Errorf("invalid Home Assistant response")
	}
	if !r.Success {
		return &connectedws.Rejected{Operation: kind}
	}
	if out != nil && json.Unmarshal(r.Result, out) != nil {
		return fmt.Errorf("invalid Home Assistant response data")
	}
	return nil
}

// Read the Home Assistant entity-state snapshot
func (d *Driver) states(ctx context.Context, session *connectedws.Session) (Sample, error) {
	var entities []entity
	if err := d.request(ctx, session, "get_states", nil, &entities); err != nil {
		return Sample{}, err
	}
	if entities == nil {
		return Sample{}, fmt.Errorf("Home Assistant returned an invalid entity snapshot")
	}
	if len(entities) > 10000 {
		return Sample{}, fmt.Errorf("Home Assistant exceeds the 10000 entity limit")
	}
	sample := Sample{Entities: map[string]entity{}}
	for _, e := range entities {
		if e.ID == "" || len(e.ID) > 512 {
			return Sample{}, fmt.Errorf("invalid Home Assistant entity ID")
		}
		if e.State == "" {
			e.State = "unknown"
		}
		sample.Entities[e.ID] = e
	}
	return sample, nil
}

// Read the Home Assistant resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	session, err := d.connection(ctx, s)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if len(d.wanted) > 10000 {
		d.mu.Unlock()
		return nil, fmt.Errorf("too many selected Home Assistant entities (maximum 10000)")
	}
	rev := d.revision
	if d.snapshot.Value != nil && d.snapshot.Fresh(rev, time.Now(), engine.RefreshInterval("home-assistant", "", s, time.Minute)) {
		sample := *d.snapshot.Value
		d.mu.Unlock()
		return sample, nil
	}
	d.mu.Unlock()
	sample, err := d.states(ctx, session)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.session != session {
		d.mu.Unlock()
		return nil, connectedws.ErrClosed
	}
	d.snapshot.Store(&sample, rev, time.Now())
	d.observed.Store(true)
	changed := d.revision != rev
	d.mu.Unlock()
	// Events during the first read invalidate its snapshot only after success
	// Check liveness to catch closure immediately before readiness changed
	if changed || !session.Alive() {
		d.notify()
	}
	return sample, nil
}

// Validate the configured entity identifier and extract its domain
func entityID(s map[string]any) (string, string, error) {
	id := strings.TrimSpace(c.String(s, "entity", ""))
	parts := strings.Split(id, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(id) > 512 {
		return "", "", fmt.Errorf("choose a Home Assistant entity")
	}
	for _, r := range id {
		if !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return "", "", fmt.Errorf("invalid Home Assistant entity ID")
		}
	}
	return id, parts[0], nil
}

// Identify Home Assistant domains supporting toggle-style commands
func switchable(domain string) bool {
	switch domain {
	case "light", "switch", "input_boolean", "fan", "humidifier":
		return true
	}
	return false
}

// Check whether the entity exposes a brightness control
func hasBrightness(e entity) bool {
	if !strings.HasPrefix(e.ID, "light.") {
		return false
	}
	for _, mode := range e.Attributes.ColorModes {
		switch mode {
		case "brightness", "color_temp", "hs", "xy", "rgb", "rgbw", "rgbww", "white":
			return true
		}
	}
	return false
}

// Invoke the selected Home Assistant domain service
func (d *Driver) service(ctx context.Context, session *connectedws.Session, domain, name string) error {
	d.mu.Lock()
	services := d.services
	if time.Since(d.servicesAt) >= time.Minute {
		services = nil
	}
	d.mu.Unlock()
	if services == nil {
		if err := d.request(ctx, session, "get_services", nil, &services); err != nil {
			return err
		}
		if services == nil {
			return fmt.Errorf("Home Assistant returned an invalid service registry")
		}
		if len(services) > 1000 {
			return fmt.Errorf("Home Assistant service registry is too large")
		}
		d.mu.Lock()
		if d.session != session {
			d.mu.Unlock()
			return connectedws.ErrClosed
		}
		d.services = services
		d.servicesAt = time.Now()
		d.mu.Unlock()
	}
	if _, ok := services[domain][name]; !ok {
		return fmt.Errorf("selected entity domain does not support %s", name)
	}
	return nil
}

// Execute the selected Home Assistant command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, ticks int) (c.Outcome, error) {
	if a == "refresh" || a == "state" {
		d.mu.Lock()
		d.services = nil
		d.servicesAt = time.Time{}
		d.mu.Unlock()
		d.notify()
		return c.Outcome{Message: "Refreshing"}, nil
	}
	id, domain, err := entityID(s)
	if err != nil {
		return c.Outcome{}, err
	}
	service := ""
	data := map[string]any{}
	switch a {
	case "on", "off", "toggle":
		if !switchable(domain) {
			return c.Outcome{}, fmt.Errorf("on/off/toggle supports lights, switches, input booleans, fans, and humidifiers")
		}
		service = map[string]string{"on": "turn_on", "off": "turn_off", "toggle": "toggle"}[a]
	case "scene":
		if domain != "scene" {
			return c.Outcome{}, fmt.Errorf("choose a scene entity")
		}
		service = "turn_on"
	case "brightness":
		if domain != "light" {
			return c.Outcome{}, fmt.Errorf("brightness requires a light entity")
		}
		service = "turn_on"
	default:
		return c.Outcome{}, fmt.Errorf("unsupported Home Assistant action")
	}
	session, err := d.connection(ctx, s)
	if err != nil {
		return c.Outcome{}, err
	}
	snapshot, err := d.states(ctx, session)
	if err != nil {
		return c.Outcome{}, err
	}
	state, exists := snapshot.Entities[id]
	if !exists {
		return c.Outcome{}, fmt.Errorf("selected Home Assistant entity no longer exists")
	}
	// A scene has a last-activation timestamp, so an unused scene may be unknown
	// Unknown device state remains explicit and cannot drive a relative dial
	if state.State == "unavailable" || state.State == "unknown" && a != "scene" {
		return c.Outcome{}, fmt.Errorf("selected Home Assistant entity is %s", state.State)
	}
	if a == "brightness" {
		if !hasBrightness(state) {
			return c.Outcome{}, fmt.Errorf("selected light does not support brightness")
		}
		step := c.Number(s, "step", 5)
		if math.IsNaN(step) || math.IsInf(step, 0) || step < 0.1 || step > 25 {
			return c.Outcome{}, fmt.Errorf("brightness step must be between 0.1 and 25 percent")
		}
		brightness := 0.0
		if state.Attributes.Brightness != nil {
			brightness = *state.Attributes.Brightness
		} else if state.State != "off" {
			return c.Outcome{}, fmt.Errorf("current light brightness is unavailable")
		}
		if math.IsNaN(brightness) || math.IsInf(brightness, 0) || brightness < 0 || brightness > 255 {
			return c.Outcome{}, fmt.Errorf("invalid current light brightness")
		}
		if ticks == 0 {
			return c.Outcome{Message: "Brightness unchanged"}, nil
		}
		data["brightness"] = int(math.Round(c.Clamp(brightness+float64(ticks)*step*255/100, 0, 255)))
	}
	if err = d.service(ctx, session, domain, service); err != nil {
		return c.Outcome{}, err
	}
	err = d.request(ctx, session, "call_service", map[string]any{"domain": domain, "service": service, "service_data": data, "target": map[string]any{"entity_id": id}}, nil)
	d.notify()
	if err != nil {
		return c.Outcome{}, connectedws.CommandError(err)
	}
	return c.Outcome{Message: "Home Assistant command accepted"}, nil
}

// List Home Assistant choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	session, err := d.connection(ctx, s)
	if err != nil {
		return nil, err
	}
	sample, err := d.states(ctx, session)
	if err != nil {
		return nil, err
	}
	choices := []c.Choice{}
	a := c.String(s, "_action", "")
	for _, e := range sample.Entities {
		domain, _, _ := strings.Cut(e.ID, ".")
		if a == "scene" && domain != "scene" || a == "brightness" && !hasBrightness(e) || (a == "on" || a == "off" || a == "toggle") && !switchable(domain) {
			continue
		}
		name := e.Attributes.Name
		if name == "" {
			name = e.ID
		} else {
			name += " (" + e.ID + ")"
		}
		if len(choices) >= 1000 {
			return nil, fmt.Errorf("Home Assistant discovery exceeds 1000 choices; enter an entity ID directly")
		}
		choices = append(choices, c.Choice{ID: e.ID, Name: name, Kind: "entity"})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].Name < choices[j].Name })
	return choices, nil
}

// Format a Home Assistant resource sample for its selected action
func (d *Driver) Display(value any, a string, s map[string]any) string {
	sample, ok := value.(Sample)
	if !ok {
		return "Home Assistant unavailable"
	}
	if a == "refresh" {
		return "Connected"
	}
	id := c.String(s, "entity", "")
	if id == "" {
		return "Choose entity"
	}
	e, exists := sample.Entities[id]
	if !exists {
		return "Entity missing"
	}
	if e.State == "unknown" || e.State == "unavailable" {
		return e.State
	}
	if a == "brightness" {
		if !hasBrightness(e) {
			return "No brightness support"
		}
		if e.State == "off" {
			return "Off"
		}
		if e.Attributes.Brightness == nil {
			return "Brightness unknown"
		}
		return fmt.Sprintf("%.0f%%", *e.Attributes.Brightness*100/255)
	}
	if e.Attributes.Unit != "" {
		return e.State + " " + e.Attributes.Unit
	}
	return e.State
}
