package obsstudio

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"math"
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

var Icons = map[string]string{"scene": "list", "mute": "speaker", "unmute": "speaker", "toggle-mute": "speaker", "volume": "speaker", "volume-display": "speaker", "record-start": "play", "record-stop": "stop", "record-pause": "pause", "record-resume": "play", "save-replay": "save", "status": "video"}
var Definition, visuals = c.Define("obs-studio", "OBS Studio", Icons, "scene", "mute", "unmute", "toggle-mute", "volume", "volume-display", "record-start", "record-stop", "record-pause", "record-resume", "save-replay", "status")

// Declare the OBS Studio global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Text("url", "WebSocket URL", "ws://127.0.0.1:4455"), metadata.Sensitive("password", "WebSocket password")}
}

// Declare settings supported by the selected OBS Studio action
func ActionFields(a string) []settings.Field {
	switch a {
	case "scene":
		return []settings.Field{metadata.Device("scene", "Scene")}
	case "mute", "unmute", "toggle-mute", "volume", "volume-display":
		f := []settings.Field{metadata.Device("input", "Input")}
		if a == "volume" {
			f = append(f, metadata.Numeric("step", "Volume percent per dial tick", 2, 0.1, 25))
		}
		return f
	}
	return nil
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 30 * time.Second, Dial: true, DialActions: engine.AdditiveActions(Definition), New: func(wake chan<- struct{}) c.Driver { return NewDriver(wake) }})

type envelope struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}
type reply struct {
	RequestID   string `json:"requestId"`
	RequestType string `json:"requestType"`
	Status      struct {
		Result bool `json:"result"`
		Code   int  `json:"code"`
	} `json:"requestStatus"`
	Data json.RawMessage `json:"responseData"`
}
type inputState struct {
	Volume float64
	Muted  bool
	Error  string
}
type recordState struct {
	Active   bool   `json:"outputActive"`
	Paused   bool   `json:"outputPaused"`
	Timecode string `json:"outputTimecode"`
}
type Sample struct {
	Scene             string
	Recording         recordState
	Replay            bool
	ReplayUnavailable bool
	Inputs            map[string]inputState
}
type Driver struct {
	client   connectedws.Client
	mu       sync.Mutex
	wake     chan<- struct{}
	dirty    atomic.Bool
	seq      atomic.Uint64
	revision uint64
	wanted   map[string]bool
	requests map[string]bool
	snapshot engine.Snapshot[Sample]
	session  *connectedws.Session
	observed *atomic.Bool
}

// Initialize the OBS Studio service driver and its wake signaling
func NewDriver(wake chan<- struct{}) *Driver { return &Driver{wake: wake, wanted: map[string]bool{}} }

// Choose the shared OBS Studio resource identity for these settings
func (*Driver) Key(map[string]any) string { return "connection" }

// Consume the OBS Studio dirty flag after a service notification
func (d *Driver) Changed() bool { return d.dirty.Swap(false) }

// Wake the OBS Studio owner loop after a state change
func (d *Driver) notify() { d.eventChanged(nil) }

// Record a ready OBS Studio notification and wake the owner loop
func (d *Driver) eventChanged(ready func() bool) {
	d.mu.Lock()
	d.revision++
	d.mu.Unlock()
	if ready != nil && !ready() {
		return
	}
	engine.SignalObservation(&d.dirty, d.wake)
}

// Retain OBS Studio resources required by the current bindings
func (d *Driver) Demand(bindings []map[string]any) {
	desired := map[string]bool{}
	for _, s := range bindings {
		if name := c.String(s, "input", ""); name != "" && len(desired) < 129 {
			desired[name] = true
		}
	}
	d.mu.Lock()
	d.wanted = desired
	d.mu.Unlock()
	d.notify()
}

// Release the OBS Studio driver resources during shutdown
func (d *Driver) Close() {
	d.client.Close()
	d.mu.Lock()
	d.session = nil
	d.observed = nil
	d.snapshot.Value = nil
	d.requests = nil
	d.mu.Unlock()
}

// Separate correlated service replies from state-change notifications
func (d *Driver) routeNotifications(raw json.RawMessage, notify func() bool) (string, error) {
	var e envelope
	if json.Unmarshal(raw, &e) != nil {
		return "", fmt.Errorf("invalid OBS message")
	}
	switch e.Op {
	case 7:
		var r reply
		if json.Unmarshal(e.D, &r) != nil || r.RequestID == "" {
			return "", fmt.Errorf("invalid OBS reply")
		}
		return r.RequestID, nil
	case 5:
		d.eventChanged(notify)
		return "", nil
	default:
		return "", fmt.Errorf("unexpected OBS message")
	}
}

// Authenticate and identify the OBS WebSocket session
func identify(ctx context.Context, conn *websocket.Conn, password string) error {
	var e envelope
	if wsjson.Read(ctx, conn, &e) != nil || e.Op != 0 {
		return fmt.Errorf("OBS WebSocket v5 hello was not received")
	}
	var hello struct {
		RPCVersion     int `json:"rpcVersion"`
		Authentication *struct {
			Challenge string `json:"challenge"`
			Salt      string `json:"salt"`
		} `json:"authentication"`
	}
	if json.Unmarshal(e.D, &hello) != nil || hello.RPCVersion < 1 {
		return fmt.Errorf("OBS server does not support WebSocket v5 RPC 1")
	}
	identify := map[string]any{"rpcVersion": 1, "eventSubscriptions": 76}
	if hello.Authentication != nil {
		if password == "" {
			return fmt.Errorf("OBS requires a WebSocket password")
		}
		secret := sha256.Sum256([]byte(password + hello.Authentication.Salt))
		auth := sha256.Sum256([]byte(base64.StdEncoding.EncodeToString(secret[:]) + hello.Authentication.Challenge))
		identify["authentication"] = base64.StdEncoding.EncodeToString(auth[:])
	}
	if wsjson.Write(ctx, conn, map[string]any{"op": 1, "d": identify}) != nil || wsjson.Read(ctx, conn, &e) != nil || e.Op != 2 {
		return fmt.Errorf("OBS authentication failed; check the WebSocket password")
	}
	var result struct {
		Negotiated int `json:"negotiatedRpcVersion"`
	}
	if json.Unmarshal(e.D, &result) != nil || result.Negotiated != 1 {
		return fmt.Errorf("OBS did not negotiate RPC version 1")
	}
	return nil
}

// Reuse or establish the configured OBS Studio service connection
func (d *Driver) connection(ctx context.Context, s map[string]any) (*connectedws.Session, error) {
	password, err := c.Secret(s, "password")
	if err != nil {
		return nil, err
	}
	endpoint := c.String(s, "url", "ws://127.0.0.1:4455")
	sum := sha256.Sum256([]byte(endpoint + "\x00" + password))
	// Until a snapshot succeeds, events only advance revision and closure stays
	// silent. Authentication followed by repeated read failures must preserve the
	// connector's failure backoff instead of scheduling immediate reconnects
	observed := &atomic.Bool{}
	session, fresh, err := d.client.Connect(ctx, hex.EncodeToString(sum[:]), endpoint,
		func(ctx context.Context, conn *websocket.Conn) error { return identify(ctx, conn, password) },
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
	var version struct {
		Available []string `json:"availableRequests"`
	}
	if err = d.request(ctx, session, "GetVersion", nil, &version); err != nil {
		d.Close()
		return nil, err
	}
	if len(version.Available) == 0 || len(version.Available) > 2048 {
		d.Close()
		return nil, fmt.Errorf("OBS did not report supported requests")
	}
	requests := map[string]bool{}
	for _, name := range version.Available {
		requests[name] = true
	}
	d.mu.Lock()
	d.requests = requests
	d.snapshot.Value = nil
	d.session = session
	d.observed = observed
	d.mu.Unlock()
	return session, nil
}

// Send a bounded OBS Studio service request and decode its response
func (d *Driver) request(ctx context.Context, session *connectedws.Session, kind string, data any, out any) error {
	id := strconv.FormatUint(d.seq.Add(1), 10)
	raw, err := session.Request(ctx, id, map[string]any{"op": 6, "d": map[string]any{"requestType": kind, "requestId": id, "requestData": data}})
	if err != nil {
		return err
	}
	var e envelope
	var r reply
	if json.Unmarshal(raw, &e) != nil || json.Unmarshal(e.D, &r) != nil || r.RequestID != id || r.RequestType != kind {
		return fmt.Errorf("invalid OBS response")
	}
	if !r.Status.Result {
		return &connectedws.Rejected{Operation: kind}
	}
	if out != nil && json.Unmarshal(r.Data, out) != nil {
		return fmt.Errorf("invalid OBS response data")
	}
	return nil
}

// Reject OBS operations whose required request types are unavailable
func (d *Driver) require(kinds ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, kind := range kinds {
		if !d.requests[kind] {
			return fmt.Errorf("OBS server does not support %s", kind)
		}
	}
	return nil
}

// Validate a required OBS source or scene selection
func selection(s map[string]any, key string) (string, error) {
	v := strings.TrimSpace(c.String(s, key, ""))
	if v == "" || len(v) > 1024 {
		return "", fmt.Errorf("choose an OBS %s", key)
	}
	return v, nil
}

// Read intentionally uses individual requests: optional replay/input failures
// have different policies from required scene/recording failures. OBS v5 batches
// (opcodes 8/9) need per-entry handling and do not provide an atomic snapshot
// https://github.com/obsproject/obs-websocket/blob/master/docs/generated/protocol.md#requestbatch-opcode-8
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	session, err := d.connection(ctx, s)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	rev := d.revision
	selectedInputPresent := false
	if d.snapshot.Value != nil {
		_, selectedInputPresent = d.snapshot.Value.Inputs[c.String(s, "input", "")]
	}
	if d.snapshot.Value != nil && (c.String(s, "input", "") == "" || selectedInputPresent) && d.snapshot.Fresh(rev, time.Now(), engine.RefreshInterval("obs-studio", "", s, 30*time.Second)) {
		sample := *d.snapshot.Value
		d.mu.Unlock()
		return sample, nil
	}
	wanted := map[string]bool{}
	for name := range d.wanted {
		wanted[name] = true
	}
	if name := c.String(s, "input", ""); name != "" {
		wanted[name] = true
	}
	d.mu.Unlock()
	if len(wanted) > 128 {
		return nil, fmt.Errorf("too many selected OBS inputs (maximum 128)")
	}
	if err = d.require("GetCurrentProgramScene", "GetRecordStatus"); err != nil {
		return nil, err
	}
	sample := Sample{Inputs: map[string]inputState{}}
	var scene struct {
		Name string `json:"currentProgramSceneName"`
	}
	if err = d.request(ctx, session, "GetCurrentProgramScene", nil, &scene); err != nil {
		return nil, err
	}
	if scene.Name == "" {
		return nil, fmt.Errorf("OBS current scene is unavailable")
	}
	sample.Scene = scene.Name
	var recording struct {
		Active   *bool  `json:"outputActive"`
		Paused   *bool  `json:"outputPaused"`
		Timecode string `json:"outputTimecode"`
	}
	if err = d.request(ctx, session, "GetRecordStatus", nil, &recording); err != nil {
		return nil, err
	}
	if recording.Active == nil || recording.Paused == nil {
		return nil, fmt.Errorf("OBS recording status is unavailable")
	}
	sample.Recording = recordState{Active: *recording.Active, Paused: *recording.Paused, Timecode: recording.Timecode}
	if d.require("GetReplayBufferStatus") != nil {
		sample.ReplayUnavailable = true
	} else if sample.Replay, err = d.replay(ctx, session); err != nil {
		// OBS rejects this read when replay buffering is disabled. Keep the
		// unrelated scene, audio, and recording observations usable
		if _, ok := errors.AsType[*connectedws.Rejected](err); !ok {
			return nil, err
		}
		sample.ReplayUnavailable = true
	}
	for name := range wanted {
		state, e := d.input(ctx, session, name)
		if e != nil {
			if errors.Is(e, connectedws.ErrClosed) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return nil, e
			}
			state.Error = e.Error()
		}
		sample.Inputs[name] = state
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

// Read the selected OBS input state
func (d *Driver) input(ctx context.Context, session *connectedws.Session, name string) (inputState, error) {
	if err := d.require("GetInputMute", "GetInputVolume"); err != nil {
		return inputState{}, err
	}
	var mute struct {
		Muted *bool `json:"inputMuted"`
	}
	var volume struct {
		Multiplier *float64 `json:"inputVolumeMul"`
	}
	data := map[string]any{"inputName": name}
	if err := d.request(ctx, session, "GetInputMute", data, &mute); err != nil {
		return inputState{}, err
	}
	if mute.Muted == nil {
		return inputState{}, fmt.Errorf("OBS input mute state is unavailable")
	}
	if err := d.request(ctx, session, "GetInputVolume", data, &volume); err != nil {
		return inputState{}, err
	}
	if volume.Multiplier == nil || math.IsNaN(*volume.Multiplier) || math.IsInf(*volume.Multiplier, 0) || *volume.Multiplier < 0 {
		return inputState{}, fmt.Errorf("OBS input volume is unavailable")
	}
	return inputState{Volume: *volume.Multiplier, Muted: *mute.Muted}, nil
}

// Read whether the OBS replay buffer is active
func (d *Driver) replay(ctx context.Context, session *connectedws.Session) (bool, error) {
	var state struct {
		Active *bool `json:"outputActive"`
	}
	if err := d.request(ctx, session, "GetReplayBufferStatus", nil, &state); err != nil {
		return false, err
	}
	if state.Active == nil {
		return false, fmt.Errorf("OBS replay buffer status is unavailable")
	}
	return *state.Active, nil
}

// Execute the selected OBS Studio command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, ticks int) (c.Outcome, error) {
	if a == "status" || a == "volume-display" {
		d.notify()
		return c.Outcome{Message: "Refreshing"}, nil
	}
	session, err := d.connection(ctx, s)
	if err != nil {
		return c.Outcome{}, err
	}
	kind := ""
	data := map[string]any{}
	switch a {
	case "scene":
		name, e := selection(s, "scene")
		if e != nil {
			return c.Outcome{}, e
		}
		kind = "SetCurrentProgramScene"
		data["sceneName"] = name
	case "mute", "unmute", "toggle-mute", "volume":
		name, e := selection(s, "input")
		if e != nil {
			return c.Outcome{}, e
		}
		data["inputName"] = name
		switch a {
		case "mute", "unmute":
			kind = "SetInputMute"
			data["inputMuted"] = a == "mute"
		case "toggle-mute":
			kind = "ToggleInputMute"
		case "volume":
			step := c.Number(s, "step", 2)
			if math.IsNaN(step) || math.IsInf(step, 0) || step < 0.1 || step > 25 {
				return c.Outcome{}, fmt.Errorf("volume step must be between 0.1 and 25 percent")
			}
			current, e := d.input(ctx, session, name)
			if e != nil {
				return c.Outcome{}, e
			}
			if ticks == 0 {
				return c.Outcome{Message: "Volume unchanged"}, nil
			}
			kind = "SetInputVolume"
			data["inputVolumeMul"] = c.Clamp(current.Volume+float64(ticks)*step/100, 0, 1)
		}
	case "record-start":
		kind = "StartRecord"
	case "record-stop":
		kind = "StopRecord"
	case "record-pause":
		kind = "PauseRecord"
	case "record-resume":
		kind = "ResumeRecord"
	case "save-replay":
		if err = d.require("GetReplayBufferStatus", "SaveReplayBuffer"); err != nil {
			return c.Outcome{}, err
		}
		active, readErr := d.replay(ctx, session)
		if readErr != nil {
			return c.Outcome{}, readErr
		}
		if !active {
			return c.Outcome{}, fmt.Errorf("start the replay buffer in OBS before saving a replay")
		}
		kind = "SaveReplayBuffer"
	default:
		return c.Outcome{}, fmt.Errorf("unsupported OBS action")
	}
	if err = d.require(kind); err != nil {
		return c.Outcome{}, err
	}
	err = d.request(ctx, session, kind, data, nil)
	d.notify()
	if err != nil {
		return c.Outcome{}, connectedws.CommandError(err)
	}
	return c.Outcome{Message: "OBS command accepted"}, nil
}

// List OBS Studio choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	session, err := d.connection(ctx, s)
	if err != nil {
		return nil, err
	}
	action := c.String(s, "_action", "")
	choices := []c.Choice{}
	if action == "scene" || action == "" {
		if err = d.require("GetSceneList"); err != nil {
			return nil, err
		}
		var list struct {
			Scenes []struct {
				Name string `json:"sceneName"`
			} `json:"scenes"`
		}
		if err = d.request(ctx, session, "GetSceneList", nil, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Scenes {
			if len(choices) >= 1000 {
				return nil, fmt.Errorf("OBS discovery exceeds 1000 choices")
			}
			choices = append(choices, c.Choice{ID: item.Name, Name: item.Name, Kind: "scene"})
		}
	}
	if action != "scene" {
		if err = d.require("GetInputList"); err != nil {
			return nil, err
		}
		var list struct {
			Inputs []struct {
				Name string `json:"inputName"`
			} `json:"inputs"`
		}
		if err = d.request(ctx, session, "GetInputList", nil, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Inputs {
			if len(choices) >= 1000 {
				return nil, fmt.Errorf("OBS discovery exceeds 1000 choices")
			}
			choices = append(choices, c.Choice{ID: item.Name, Name: item.Name, Kind: "input"})
		}
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].Name < choices[j].Name })
	return choices, nil
}

// Format a OBS Studio resource sample for its selected action
func (d *Driver) Display(value any, a string, s map[string]any) string {
	sample, ok := value.(Sample)
	if !ok {
		return "OBS unavailable"
	}
	switch a {
	case "scene":
		if sample.Scene == "" {
			return "No scene"
		}
		return sample.Scene
	case "mute", "unmute", "toggle-mute", "volume", "volume-display":
		state, exists := sample.Inputs[c.String(s, "input", "")]
		if !exists {
			return "Choose input"
		}
		if state.Error != "" {
			return "Input unavailable"
		}
		if state.Muted {
			return "Muted"
		}
		return fmt.Sprintf("%.0f%%", state.Volume*100)
	case "save-replay":
		if sample.ReplayUnavailable {
			return "Replay unavailable"
		}
		if sample.Replay {
			return "Replay ready"
		}
		return "Replay inactive"
	default:
		if sample.Recording.Paused {
			return "Recording paused"
		}
		if sample.Recording.Active {
			if sample.Recording.Timecode != "" {
				return "REC " + sample.Recording.Timecode
			}
			return "Recording"
		}
		return "Recording stopped"
	}
}
