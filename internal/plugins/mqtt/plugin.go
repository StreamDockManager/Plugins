package mqtt

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	paho "github.com/eclipse/paho.mqtt.golang"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var Icons = map[string]string{"publish": "upload", "text": "list", "status": "network"}
var Definition, visuals = c.DefineVault("mqtt", "MQTT", Icons, "publish", "text", "status")

// Declare the MQTT global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{
		metadata.Text("broker", "Broker URL (tcp:// or tls://)", "tcp://127.0.0.1:1883"),
		metadata.Text("client_id", "Client ID (empty generates a process ID)", ""),
		metadata.Text("username", "Username (or ${ENV_NAME})", ""),
		metadata.Sensitive("password", "Password"),
		metadata.Path("ca_file", "CA certificate file (optional PEM)", "", "file"),
		metadata.Path("client_cert", "Client certificate file (optional PEM)", "", "file"),
		metadata.Path("client_key", "Client key file (optional PEM)", "", "file"),
	}
}

// Declare settings supported by the selected MQTT action
func ActionFields(a string) []settings.Field {
	if a == "status" {
		return nil
	}
	f := []settings.Field{metadata.Text("topic", "MQTT topic", ""), metadata.Select("qos", "QoS", "0", "0", "1", "2")}
	if a == "publish" {
		return append(f, metadata.Text("payload", "Payload", ""), metadata.Check("retain", "Retain published message"))
	}
	return append(f, metadata.Text("json_path", "JSON field path (empty displays text)", ""))
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: time.Minute, New: func(wake chan<- struct{}) c.Driver { return NewDriver(wake) }})

type Message struct {
	Topic, Text  string
	Rejection    string
	PayloadBytes int
	Retained     bool
	At           time.Time
	parsed       *messageJSON
	order        uint64
}
type Sample struct {
	Connected bool
	Messages  map[string]Message
}
type Driver struct {
	vaultSession  c.VaultSession
	paths         map[string][]string
	mu            sync.Mutex
	client        paho.Client
	subscriptions map[string]byte
	messages      map[string]Message
	snapshots     map[string]map[string]Message
	wake          chan<- struct{}
	dirty         atomic.Bool
	generation    uint64
	clientID      string
	desired       map[string]byte
	demandByScope map[string]map[string]byte
	activeScope   string
	demandKnown   bool
	receiving     map[string]byte
	budget        *messageBudget
	messageOrder  uint64
}

// Initialize the MQTT service driver and its wake signaling
func NewDriver(wake chan<- struct{}) *Driver {
	var id [8]byte
	_, _ = rand.Read(id[:])
	return &Driver{wake: wake, subscriptions: map[string]byte{}, messages: map[string]Message{}, budget: &messageBudget{}, clientID: "stream-dock-manager-" + hex.EncodeToString(id[:])}
}

// Choose the shared MQTT resource identity for these settings
func (d *Driver) Key(s map[string]any) string {
	return c.VaultKey(s, Definition.Slug) + d.resourceKey(s)
}
func (d *Driver) resourceKey(s map[string]any) string {
	return "topic:" + c.String(s, "topic", "") + ":" + c.String(s, "qos", "0")
}

// Consume the MQTT dirty flag after a service notification
func (d *Driver) Changed() bool { return d.dirty.Swap(false) }

// Retain MQTT resources required by the current bindings
func (d *Driver) Demand(bindings []map[string]any) {
	byScope := map[string]map[string]byte{}
	paths := map[string][]string{}
	for _, s := range bindings {
		if c.String(s, "_action", "") != "text" {
			continue
		}
		topic := c.String(s, "topic", "")
		if topic == "" {
			continue
		}
		if path := c.String(s, "json_path", ""); path != "" && len(path) <= 1024 && len(paths[topic]) < 64 && !slices.Contains(paths[topic], path) {
			paths[topic] = append(paths[topic], path)
		}
		q, err := qos(s)
		if err != nil {
			q = 255
		}
		scope := c.VaultKey(s, Definition.Slug)
		desired := byScope[scope]
		if desired == nil {
			desired = map[string]byte{}
			byScope[scope] = desired
		}
		prior, ok := desired[topic]
		if !ok || q > prior {
			desired[topic] = q
		}
	}
	d.mu.Lock()
	changed := !d.demandKnown || !reflect.DeepEqual(byScope, d.demandByScope)
	d.demandByScope = byScope
	d.paths = paths
	d.desired = byScope[d.activeScope]
	d.demandKnown = true
	d.pruneMessages()
	d.mu.Unlock()
	if changed {
		d.notify()
	}
}

// Wake the MQTT owner loop after a state change
func (d *Driver) notify() {
	d.dirty.Store(true)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Wait for an MQTT operation token while respecting cancellation
func wait(ctx context.Context, token paho.Token) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		if token.Error() != nil {
			return fmt.Errorf("MQTT operation failed; check broker authentication and topic permissions")
		}
		return nil
	}
}

// Reuse or establish the configured MQTT service connection
func (d *Driver) connection(ctx context.Context, s map[string]any) (paho.Client, error) {
	d.mu.Lock()
	existing := d.client
	d.mu.Unlock()
	if existing != nil && existing.IsConnectionOpen() {
		return existing, nil
	}
	d.Close()
	broker, err := url.Parse(c.String(s, "broker", "tcp://127.0.0.1:1883"))
	if err != nil || broker.Hostname() == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" || (broker.Path != "" && broker.Path != "/") {
		return nil, fmt.Errorf("invalid MQTT broker URL")
	}
	if broker.Scheme == "tls" {
		broker.Scheme = "ssl"
	}
	if broker.Scheme != "tcp" && broker.Scheme != "ssl" {
		return nil, fmt.Errorf("MQTT requires tcp:// or tls://")
	}
	config := &tls.Config{ServerName: broker.Hostname(), MinVersion: tls.VersionTLS12}
	if ca := c.String(s, "ca_file", ""); ca != "" {
		data, err := os.ReadFile(ca)
		if err != nil || len(data) > 1<<20 {
			return nil, fmt.Errorf("cannot read MQTT CA certificate")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("invalid MQTT CA certificate")
		}
		config.RootCAs = pool
	}
	cert, key := c.String(s, "client_cert", ""), c.String(s, "client_key", "")
	if cert != "" || key != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("invalid MQTT client certificate/key pair")
		}
		config.Certificates = []tls.Certificate{pair}
	}
	user, err := c.Secret(s, "username")
	if err != nil {
		return nil, err
	}
	password, err := c.Secret(s, "password")
	if err != nil {
		return nil, err
	}
	id := c.String(s, "client_id", "")
	if id == "" {
		id = d.clientID
	}
	options := paho.NewClientOptions().AddBroker(broker.String()).SetClientID(id).SetUsername(user).SetPassword(password).SetProtocolVersion(4).SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).SetResumeSubs(false).SetKeepAlive(30 * time.Second).SetPingTimeout(5 * time.Second).SetWriteTimeout(5 * time.Second).SetConnectTimeout(5 * time.Second).SetOrderMatters(false).SetTLSConfig(config)
	// Keep broker dialing under the configured transport and bounded packet reader
	options.SetCustomOpenConnectionFn(func(uri *url.URL, _ paho.ClientOptions) (net.Conn, error) {
		address := uri.Host
		if uri.Port() == "" {
			port := "1883"
			if uri.Scheme == "ssl" {
				port = "8883"
			}
			address = net.JoinHostPort(uri.Hostname(), port)
		}
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		if uri.Scheme == "ssl" {
			secured := tls.Client(conn, config)
			if err = secured.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			conn = secured
		}
		return &packetConn{Conn: sdk.TrackConn(conn)}, nil
	})
	d.mu.Lock()
	d.generation++
	generation := d.generation
	d.mu.Unlock()
	// Ignore messages from obsolete connections before updating retained snapshots
	options.SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) {
		if d.receiveMessage(generation, m.Topic(), m.Payload(), m.Retained()) {
			d.notify()
		}
	})
	options.SetConnectionLostHandler(func(paho.Client, error) { d.notify() })
	cl := paho.NewClient(options)
	d.mu.Lock()
	if d.generation != generation {
		d.mu.Unlock()
		return nil, context.Canceled
	}
	d.client = cl
	d.mu.Unlock()
	if err = wait(ctx, cl.Connect()); err != nil {
		d.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		d.Close()
		return nil, ctx.Err()
	}
	return cl, nil
}

// Validate the configured MQTT quality-of-service level
func qos(s map[string]any) (byte, error) {
	n, err := strconv.Atoi(c.String(s, "qos", "0"))
	if err != nil || n < 0 || n > 2 {
		return 0, fmt.Errorf("MQTT QoS must be 0, 1 or 2")
	}
	return byte(n), nil
}

// Read the MQTT resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	return c.WithVault(ctx, s, Definition.Slug, &d.vaultSession, d.Close, d.readService)
}
func (d *Driver) readService(ctx context.Context, s map[string]any) (any, error) {
	cl, err := d.connection(ctx, s)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.activeScope = c.VaultKey(s, Definition.Slug)
	d.desired = d.demandByScope[d.activeScope]
	desired, known := maps.Clone(d.desired), d.demandKnown
	subscriptions := maps.Clone(d.subscriptions)
	d.mu.Unlock()
	if !known {
		desired = map[string]byte{}
		if topic := c.String(s, "topic", ""); c.String(s, "_action", "text") == "text" && topic != "" {
			q, err := qos(s)
			if err != nil {
				return nil, err
			}
			desired[topic] = q
		}
	}
	d.mu.Lock()
	d.receiving = desired
	d.pruneMessages()
	d.mu.Unlock()
	for topic := range subscriptions {
		if _, needed := desired[topic]; !needed {
			if err = wait(ctx, cl.Unsubscribe(topic)); err != nil {
				d.Close()
				return nil, err
			}
			d.mu.Lock()
			delete(d.subscriptions, topic)
			d.mu.Unlock()
		}
	}
	for topic, q := range desired {
		if q > 2 {
			return nil, fmt.Errorf("MQTT QoS must be 0, 1 or 2")
		}
		existing, subscribed := subscriptions[topic]
		if !subscribed || existing != q {
			token := cl.Subscribe(topic, q, nil)
			if err = wait(ctx, token); err != nil {
				d.Close()
				return nil, err
			}
			if sub, ok := token.(*paho.SubscribeToken); ok {
				granted, found := sub.Result()[topic]
				if !found || granted > 2 {
					d.Close()
					return nil, fmt.Errorf("MQTT broker denied this subscription")
				}
			}
			d.mu.Lock()
			d.subscriptions[topic] = q
			d.mu.Unlock()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return Sample{Connected: cl.IsConnectionOpen(), Messages: d.snapshot(c.String(s, "topic", ""))}, nil
}

// Execute the selected MQTT command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, ticks int) (c.Outcome, error) {
	return c.WithVault(ctx, s, Definition.Slug, &d.vaultSession, d.Close, func(ctx context.Context, s map[string]any) (c.Outcome, error) {
		return d.commandService(ctx, a, s, ticks)
	})
}
func (d *Driver) commandService(ctx context.Context, a string, s map[string]any, _ int) (c.Outcome, error) {
	if a != "publish" {
		return c.Outcome{Message: "MQTT refresh requested"}, nil
	}
	topic := c.String(s, "topic", "")
	if topic == "" || strings.ContainsAny(topic, "+#\x00") || len(topic) > 65535 {
		return c.Outcome{}, fmt.Errorf("publish needs a concrete MQTT topic")
	}
	payload := c.String(s, "payload", "")
	if len(payload) > 64<<10 {
		return c.Outcome{}, fmt.Errorf("MQTT payload exceeds 64 KiB")
	}
	q, err := qos(s)
	if err != nil {
		return c.Outcome{}, err
	}
	cl, err := d.connection(ctx, s)
	if err != nil {
		return c.Outcome{}, err
	}
	if !cl.IsConnectionOpen() {
		return c.Outcome{}, fmt.Errorf("MQTT disconnected; message was not queued")
	}
	err = wait(ctx, cl.Publish(topic, q, c.Boolean(s, "retain"), payload))
	if err != nil {
		d.Close()
		return c.Outcome{}, fmt.Errorf("MQTT delivery uncertain; message will not be replayed automatically")
	}
	message := "Message sent (QoS 0; no broker acknowledgement)"
	if q > 0 {
		message = "Broker acknowledged message"
	}
	return c.Outcome{Message: message}, nil
}

// List MQTT choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	return c.WithVault(ctx, s, Definition.Slug, &d.vaultSession, d.Close, d.discoverService)
}
func (d *Driver) discoverService(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	_, err := d.connection(ctx, s)
	return nil, err
}

// Format a MQTT resource sample for its selected action
func (d *Driver) Display(value any, a string, s map[string]any) string {
	v := value.(Sample)
	if !v.Connected {
		return "MQTT disconnected"
	}
	if a != "text" {
		return "MQTT broker connected"
	}
	topic := c.String(s, "topic", "")
	message, ok := v.Messages[topic]
	if !ok {
		for _, candidate := range v.Messages {
			if matches(topic, candidate.Topic) && (!ok || candidate.At.After(message.At)) {
				message, ok = candidate, true
			}
		}
	}
	if !ok {
		return "Subscribed · waiting for message"
	}
	if message.Rejection != "" {
		return fmt.Sprintf("%s · received %s\nRejected: %s (%d bytes)", message.Topic, message.At.UTC().Format(time.RFC3339), message.Rejection, message.PayloadBytes)
	}
	text := message.Text
	if path := c.String(s, "json_path", ""); path != "" {
		d.mu.Lock()
		paths := d.paths
		d.mu.Unlock()
		extracted := message.extractPaths(path, paths)
		text = extracted.text
		if !extracted.valid {
			return text
		}
	}
	label := message.Topic
	if message.Retained {
		label += " · retained"
	}
	return label + " · received " + message.At.UTC().Format(time.RFC3339) + "\n" + text
}

// Check whether an MQTT topic matches a subscription filter
func matches(filter, topic string) bool {
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	if strings.HasPrefix(topic, "$") && !strings.HasPrefix(filter, "$") {
		return false
	}
	for i, part := range f {
		if part == "#" {
			return i == len(f)-1
		}
		if i >= len(t) || (part != "+" && part != t[i]) {
			return false
		}
	}
	return len(f) == len(t)
}

// Release the MQTT driver resources during shutdown
func (d *Driver) Close() {
	d.mu.Lock()
	cl := d.client
	d.client = nil
	d.generation++
	clear(d.subscriptions)
	for topic := range d.messages {
		d.removeMessage(topic)
	}
	clear(d.snapshots)
	clear(d.receiving)
	d.mu.Unlock()
	if cl != nil {
		cl.Disconnect(0)
	}
}

// Validate Remaining Length before the client library allocates a packet body
type packetConn struct {
	net.Conn
	header    []byte
	remaining int
}

// Read MQTT packet bytes while enforcing the packet-size limit
func (c *packetConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.header) == 0 && c.remaining == 0 {
		var b [1]byte
		if _, err := io.ReadFull(c.Conn, b[:]); err != nil {
			return 0, err
		}
		c.header = append(c.header, b[0])
		multiplier := 1
		for i := range 4 {
			if _, err := io.ReadFull(c.Conn, b[:]); err != nil {
				return 0, err
			}
			c.header = append(c.header, b[0])
			c.remaining += int(b[0]&127) * multiplier
			if c.remaining > 1<<20 {
				return 0, fmt.Errorf("MQTT packet exceeds 1 MiB")
			}
			if b[0]&128 == 0 {
				break
			}
			if i == 3 {
				return 0, fmt.Errorf("invalid MQTT Remaining Length")
			}
			multiplier *= 128
		}
	}
	if len(c.header) > 0 {
		n := copy(p, c.header)
		c.header = c.header[n:]
		return n, nil
	}
	n, err := c.Conn.Read(p[:min(len(p), c.remaining)])
	c.remaining -= n
	return n, err
}
