package obsstudio

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/connectors/testutil"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var allRequests = []string{"GetVersion", "GetCurrentProgramScene", "GetRecordStatus", "GetReplayBufferStatus", "GetSceneList", "GetInputList", "GetInputMute", "GetInputVolume", "SetCurrentProgramScene", "SetInputMute", "ToggleInputMute", "SetInputVolume", "StartRecord", "StopRecord", "PauseRecord", "ResumeRecord", "SaveReplayBuffer"}

type obsFixture struct {
	mu             sync.Mutex
	url            string
	connections    atomic.Int32
	commands       []string
	volume         float64
	mute           bool
	replay         bool
	replayDisabled bool
	scene          string
	recording      bool
	paused         bool
	uncertain      bool
	unsupported    string
	interleave     bool
	volumeMissing  bool
	missingField   string
	setupFailure   string
	disconnectRead bool
}

// Start a fake OBS WebSocket service for protocol tests
func newOBS(t *testing.T) *obsFixture {
	t.Helper()
	f := &obsFixture{volume: 0.5, scene: "Main", interleave: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1 << 20)
		f.connections.Add(1)
		_ = wsjson.Write(context.Background(), conn, map[string]any{"op": 0, "d": map[string]any{"rpcVersion": 1, "authentication": map[string]any{"salt": "salt", "challenge": "challenge"}}})
		var identify struct {
			Op int `json:"op"`
			D  struct {
				Authentication string `json:"authentication"`
				RPC            int    `json:"rpcVersion"`
				Subscriptions  int    `json:"eventSubscriptions"`
			} `json:"d"`
		}
		if wsjson.Read(context.Background(), conn, &identify) != nil {
			return
		}
		secret := sha256.Sum256([]byte("fixture-passwordsalt"))
		want := sha256.Sum256([]byte(base64.StdEncoding.EncodeToString(secret[:]) + "challenge"))
		if identify.Op != 1 || identify.D.RPC != 1 || identify.D.Authentication != base64.StdEncoding.EncodeToString(want[:]) || identify.D.Subscriptions != 76 {
			return
		}
		_ = wsjson.Write(context.Background(), conn, map[string]any{"op": 2, "d": map[string]any{"negotiatedRpcVersion": 1}})
		for {
			var request struct {
				Op int `json:"op"`
				D  struct {
					ID   string         `json:"requestId"`
					Type string         `json:"requestType"`
					Data map[string]any `json:"requestData"`
				} `json:"d"`
			}
			if wsjson.Read(context.Background(), conn, &request) != nil {
				return
			}
			if request.Op != 6 {
				t.Errorf("unexpected OBS opcode %d: serial requests require opcode 6", request.Op)
				return
			}

			f.mu.Lock()
			data := map[string]any{}
			success := true
			closeNow := false
			switch request.D.Type {
			case "GetVersion":
				requests := []string{}
				for _, name := range allRequests {
					if name != f.unsupported {
						requests = append(requests, name)
					}
				}
				data["availableRequests"] = requests
				switch f.setupFailure {
				case "rejected":
					success = false
				case "missing":
					delete(data, "availableRequests")
				case "malformed":
					data["availableRequests"] = "invalid"
				case "disconnected":
					closeNow = true
				}
			case "GetCurrentProgramScene":
				data["currentProgramSceneName"] = f.scene
				closeNow = f.disconnectRead
			case "GetRecordStatus":
				data["outputActive"] = f.recording
				data["outputPaused"] = f.paused
				data["outputTimecode"] = "00:00:12.000"
			case "GetReplayBufferStatus":
				data["outputActive"] = f.replay
				success = !f.replayDisabled
			case "GetSceneList":
				data["scenes"] = []any{map[string]any{"sceneName": "Main"}, map[string]any{"sceneName": "Other"}}
			case "GetInputList":
				data["inputs"] = []any{map[string]any{"inputName": "Mic"}}
			case "GetInputMute":
				data["inputMuted"] = f.mute
			case "GetInputVolume":
				if !f.volumeMissing {
					data["inputVolumeMul"] = f.volume
				}
			default:
				f.commands = append(f.commands, request.D.Type)
				closeNow = f.uncertain
				switch request.D.Type {
				case "SetInputVolume":
					f.volume = request.D.Data["inputVolumeMul"].(float64)
				case "SetInputMute":
					f.mute = request.D.Data["inputMuted"].(bool)
				case "ToggleInputMute":
					f.mute = !f.mute
				case "SetCurrentProgramScene":
					if request.D.Data["sceneName"] == "Missing" {
						success = false
					} else {
						f.scene = request.D.Data["sceneName"].(string)
					}
				case "StartRecord":
					f.recording = true
				case "StopRecord":
					f.recording = false
					f.paused = false
				case "PauseRecord":
					f.paused = true
				case "ResumeRecord":
					f.paused = false
				}
			}
			delete(data, f.missingField)
			interleave := f.interleave || request.D.Type == "GetCurrentProgramScene" && f.disconnectRead
			f.interleave = false
			f.mu.Unlock()
			if interleave {
				_ = wsjson.Write(context.Background(), conn, map[string]any{"op": 5, "d": map[string]any{"eventType": "CurrentProgramSceneChanged", "eventData": map[string]any{"sceneName": "Main"}}})
			}
			if closeNow {
				return
			}
			_ = wsjson.Write(context.Background(), conn, map[string]any{"op": 7, "d": map[string]any{"requestId": request.D.ID, "requestType": request.D.Type, "requestStatus": map[string]any{"result": success, "code": 100}, "responseData": data}})
		}
	}))
	t.Cleanup(server.Close)
	f.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return f
}

// Build OBS Studio connection settings for the test service
func (f *obsFixture) settings() map[string]any {
	return map[string]any{"url": f.url, "password": "fixture-password", "input": "Mic", "scene": "Other", "_action": "volume"}
}

// Verify authentication events shared connection commands and dials
func TestAuthenticationEventsSharedConnectionCommandsAndDials(t *testing.T) {
	f := newOBS(t)
	d := NewDriver(make(chan struct{}, 1))
	defer d.Close()
	s := f.settings()
	d.Demand([]map[string]any{s})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent, received := sdk.NetworkPayload()
	value, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(value, "volume-display", s) != "50%" {
		t.Fatal(value)
	}
	if !d.Changed() {
		t.Fatal("event notification was lost")
	}
	if _, err = d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	choices, err := d.Discover(ctx, s)
	if err != nil || len(choices) != 1 || choices[0].ID != "Mic" {
		t.Fatal(choices, err)
	}
	s["_action"] = "scene"
	choices, err = d.Discover(ctx, s)
	if err != nil || len(choices) != 2 {
		t.Fatal(choices, err)
	}
	for _, a := range []string{"scene", "mute", "unmute", "toggle-mute", "record-start", "record-pause", "record-resume", "record-stop"} {
		if _, err = d.Command(ctx, a, s, 0); err != nil {
			t.Fatal(a, err)
		}
	}
	if _, err = d.Command(ctx, "volume", s, 1000); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	v := f.volume
	f.mu.Unlock()
	if v != 1 {
		t.Fatal("volume did not clamp", v)
	}
	if _, err = d.Command(ctx, "volume", s, -1000); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	v = f.volume
	f.mu.Unlock()
	if v != 0 {
		t.Fatal("volume did not clamp", v)
	}
	if _, err = d.Command(ctx, "save-replay", s, 0); err == nil {
		t.Fatal("inactive replay buffer accepted")
	}
	f.mu.Lock()
	f.replay = true
	f.mu.Unlock()
	if _, err = d.Command(ctx, "save-replay", s, 0); err != nil {
		t.Fatal(err)
	}
	if f.connections.Load() != 1 {
		t.Fatal("connection not shared", f.connections.Load())
	}
	afterSent, afterReceived := sdk.NetworkPayload()
	if afterSent <= sent || afterReceived <= received {
		t.Fatal("WebSocket traffic not tracked")
	}
	f.mu.Lock()
	for _, command := range f.commands {
		if command == "StartReplayBuffer" {
			t.Fatal("replay buffer was implicitly started")
		}
	}
	f.mu.Unlock()
}

// Verify no replay after uncertain delivery and fresh reconnect
func TestNoReplayAfterUncertainDeliveryAndFreshReconnect(t *testing.T) {
	f := newOBS(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := d.Read(ctx, s); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.uncertain = true
	f.mu.Unlock()
	if _, err := d.Command(ctx, "scene", s, 0); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.uncertain = false
	f.mu.Unlock()
	value, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(value, "scene", s) != "Other" {
		t.Fatal("reconnect reused stale scene", value)
	}
	f.mu.Lock()
	count := len(f.commands)
	f.mu.Unlock()
	if count != 1 || f.connections.Load() != 2 {
		t.Fatal("write replayed", count, f.connections.Load())
	}
	d.Close()
	s["password"] = "wrong-secret"
	if _, err = d.Read(ctx, s); err == nil || strings.Contains(err.Error(), "wrong-secret") {
		t.Fatal("authentication error", err)
	}
}

// Verify missing capabilities and malformed volume never write
func TestMissingCapabilitiesAndMalformedVolumeNeverWrite(t *testing.T) {
	f := newOBS(t)
	f.unsupported = "SetInputVolume"
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	if _, err := d.Command(context.Background(), "volume", s, 1); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatal(err)
	}
	d.Close()
	f.mu.Lock()
	f.unsupported = ""
	f.volumeMissing = true
	f.mu.Unlock()
	if _, err := d.Command(context.Background(), "volume", s, 1); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal(err)
	}
	f.mu.Lock()
	count := len(f.commands)
	f.mu.Unlock()
	if count != 0 {
		t.Fatal("invalid volume triggered a write")
	}
}

// Verify event invalidates snapshot and demand drops input
func TestEventInvalidatesSnapshotAndDemandDropsInput(t *testing.T) {
	f := newOBS(t)
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	d.Demand([]map[string]any{s})
	first, err := d.Read(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.volume = 0.9
	f.mu.Unlock()
	_, err = d.routeNotifications(json.RawMessage(`{"op":5,"d":{"eventType":"InputVolumeChanged"}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := d.Read(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(next, "volume", s) != "90%" || d.Display(first, "volume", s) != "50%" {
		t.Fatal("snapshot mutated or event ignored")
	}
	d.Demand(nil)
	delete(s, "input")
	next, err = d.Read(context.Background(), s)
	if err != nil || len(next.(Sample).Inputs) != 0 {
		t.Fatal("unbound input retained", next, err)
	}
	d.Close()
	testutil.Eventually(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.snapshot.Value == nil })
}

// Verify absent statuses remain unavailable
func TestAbsentStatusesRemainUnavailable(t *testing.T) {
	for _, field := range []string{"outputActive", "outputPaused", "inputMuted"} {
		t.Run(field, func(t *testing.T) {
			f := newOBS(t)
			f.missingField = field
			d := NewDriver(nil)
			defer d.Close()
			s := f.settings()
			value, err := d.Read(context.Background(), s)
			if field == "inputMuted" {
				if err != nil {
					t.Fatal(err)
				}
				if d.Display(value, "volume", s) != "Input unavailable" {
					t.Fatal("absent mute state became unmuted", value)
				}
			} else if err == nil {
				t.Fatal("absent output state became stopped", value)
			}
		})
	}
}

// Verify disabled replay does not disable unrelated actions
func TestDisabledReplayDoesNotDisableUnrelatedActions(t *testing.T) {
	f := newOBS(t)
	f.replayDisabled = true
	d := NewDriver(nil)
	defer d.Close()
	s := f.settings()
	value, err := d.Read(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Display(value, "volume", s) != "50%" || d.Display(value, "save-replay", s) != "Replay unavailable" {
		t.Fatal("disabled replay poisoned shared observations", value)
	}
	if _, err = d.Command(context.Background(), "record-start", s, 0); err != nil {
		t.Fatal("recording blocked by disabled replay", err)
	}
	if _, err = d.Command(context.Background(), "save-replay", s, 0); err == nil {
		t.Fatal("disabled replay accepted save")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, command := range f.commands {
		if command == "SaveReplayBuffer" || command == "StartReplayBuffer" {
			t.Fatal("disabled replay buffer was mutated", command)
		}
	}
}

// Verify setup failures preserve failure backoff
func TestSetupFailuresPreserveFailureBackoff(t *testing.T) {
	for _, failure := range []string{"rejected", "missing", "malformed", "disconnected"} {
		t.Run(failure, func(t *testing.T) {
			f := newOBS(t)
			f.setupFailure = failure
			wake := make(chan struct{}, 1)
			d := NewDriver(wake)
			defer d.Close()
			s := f.settings()
			for attempt := range 3 {
				f.mu.Lock()
				f.interleave = true
				f.mu.Unlock()
				if _, err := d.Read(context.Background(), s); err == nil {
					t.Fatal("failed setup accepted")
				}
				if d.Changed() {
					t.Fatal("failed setup invalidated observations and bypassed retry backoff", failure, attempt)
				}
				select {
				case <-wake:
					t.Fatal("failed setup woke connector")
				default:
				}
			}
			f.mu.Lock()
			f.setupFailure = ""
			f.mu.Unlock()
			if _, err := d.Read(context.Background(), s); err != nil {
				t.Fatal("recovery failed", err)
			}
			d.Changed()
			d.Close()
			if !d.Changed() {
				t.Fatal("initialized connection closure did not invalidate observations")
			}
		})
	}
}

// Verify disconnect before first snapshot preserves failure backoff
func TestDisconnectBeforeFirstSnapshotPreservesFailureBackoff(t *testing.T) {
	f := newOBS(t)
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
	if f.connections.Load() != 4 {
		t.Fatal("fixture did not exercise authenticated reconnects", f.connections.Load())
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
