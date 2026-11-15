package randommeme

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type step func(context.Context, *sdk.Client) error
type fixtureDriver struct {
	*engine.Engine
	steps []step
}

// Keep the random meme display fixture idle between explicit test steps
func (d *fixtureDriver) Poll(context.Context, *sdk.Client) error { return nil }

// Execute the requested random meme fixture step and signal its completion
func (d *fixtureDriver) Handle(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	var index int
	if err := json.Unmarshal(ev.Payload, &index); err != nil {
		return err
	}
	err := d.steps[index](ctx, c)
	deadline := time.Now().Add(2 * time.Second)
	for len(d.Jobs) > 0 && time.Now().Before(deadline) {
		if v := d.DrainJobs(ctx, c); v != nil {
			err = v
			break
		}
		time.Sleep(time.Millisecond)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return c.Send(ctx, sdk.Message("fixtureDone", "", message))
}

// Run meme lifecycle steps and capture their emitted host events
func runSteps(t *testing.T, e *engine.Engine, steps []step) [][]sdk.Event {
	t.Helper()
	result := make(chan [][]sdk.Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1 << 20)
		wsCtx, wsCancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Second))
		defer wsCancel()
		var registration sdk.Event
		if err := wsjson.Read(wsCtx, conn, &registration); err != nil {
			t.Error(err)
			return
		}
		all := make([][]sdk.Event, len(steps))
		for i := range steps {
			if err := wsjson.Write(wsCtx, conn, sdk.Message("fixtureStep", "", i)); err != nil {
				t.Error(err)
				return
			}
			for {
				var event sdk.Event
				if err := wsjson.Read(wsCtx, conn, &event); err != nil {
					t.Error(err)
					return
				}
				if event.Event == "fixtureDone" {
					if string(event.Payload) != `""` {
						t.Error("step failed", i, string(event.Payload))
					}
					break
				}
				all[i] = append(all[i], event)
			}
		}
		result <- all
		_ = wsjson.Write(wsCtx, conn, sdk.Event{Event: "shutdown"})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := testhost.Connect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "meme-fixture", &fixtureDriver{Engine: e, steps: steps}); err != nil {
		t.Fatal(err)
	}
	select {
	case all := <-result:
		return all
	case <-ctx.Done():
		t.Fatal("fixture timed out")
		return nil
	}
}

// Verify fetch preview then paste link only on send
func TestFetchPreviewThenPasteLinkOnlyOnSend(t *testing.T) {
	var p *Plugin
	e := fixtureengine.New(Definition, filepath.Join(t.TempDir(), "state.json"), func(e *engine.Engine) engine.Feature {
		api, _, _ := fixtureAPI(t, []Meme{{URL: "https://i.redd.it/meme.png", Title: "Example"}})
		p = &Plugin{Engine: e, API: api, Current: map[string]*Meme{}, Pending: map[string]pendingInput{}}
		return p.hooks()
	})
	for id, action := range map[string]string{"fetch": "fetch", "send": "send", "other": "send"} {
		key := "memes"
		if id == "other" {
			key = "separate"
		}
		e.Bindings[id] = engine.Binding{Action: action, Settings: map[string]any{"shared_id": key}, Surface: &sdk.Surface{Width: 64, Height: 64}}
	}
	press := func(id string) step {
		return func(ctx context.Context, c *sdk.Client) error {
			return e.Handle(ctx, c, sdk.Event{Event: "keyDown", Context: id, Action: e.Bindings[id].Action})
		}
	}
	all := runSteps(t, e, []step{
		func(ctx context.Context, c *sdk.Client) error {
			for _, id := range []string{"fetch", "send"} {
				if err := e.Handle(ctx, c, sdk.Event{Event: "willAppear", Context: id, Action: e.Bindings[id].Action, Payload: json.RawMessage(`{"settings":{"extension":"retained"}}`), Surface: &sdk.Surface{Width: 64, Height: 64}}); err != nil {
					return err
				}
				if e.Bindings[id].Settings["shared_id"] != "memes" || e.Bindings[id].Settings["extension"] != "retained" {
					return fmt.Errorf("runtime group default lost or altered settings")
				}
			}
			return nil
		},
		func(ctx context.Context, c *sdk.Client) error {
			return e.Handle(ctx, c, sdk.Event{Event: "sendToPlugin", Context: "send", Payload: json.RawMessage(`{"op":"test"}`)})
		},
		func(ctx context.Context, c *sdk.Client) error {
			if err := press("send")(ctx, c); err == nil {
				return fmt.Errorf("Send accepted before Fetch")
			}
			return nil
		},
		press("fetch"),
		func(ctx context.Context, c *sdk.Client) error {
			if p.current(e.Bindings["send"]) == nil {
				return fmt.Errorf("Fetch did not populate shared preview")
			}
			if err := press("other")(ctx, c); err == nil {
				return fmt.Errorf("unrelated group could send")
			}
			return nil
		},
		press("send"),
		press("send"),
		func(ctx context.Context, c *sdk.Client) error {
			if err := e.Handle(ctx, c, sdk.Event{Event: "desktopInputResult", Context: "send", Payload: json.RawMessage(`{"request_id":"unrelated","outcome":"Transcript inserted"}`)}); err != nil {
				return err
			}
			if len(p.Pending) != 1 {
				return fmt.Errorf("uncorrelated reply completed Send")
			}
			return nil
		},
		func(ctx context.Context, c *sdk.Client) error {
			payload, _ := json.Marshal(map[string]any{"request_id": p.Pending["send"].request, "outcome": "Transcript inserted"})
			if err := e.Handle(ctx, c, sdk.Event{Event: "desktopInputResult", Context: "send", Payload: payload}); err != nil {
				return err
			}
			if p.ResultsText["send"] != "Pasted" || len(p.Pending) != 0 {
				return fmt.Errorf("correlated input completion failed")
			}
			return nil
		},
	})
	for i, events := range all {
		inputs := []map[string]any{}
		for _, event := range events {
			if event.Event == "desktopInput" {
				var v map[string]any
				_ = json.Unmarshal(event.Payload, &v)
				inputs = append(inputs, v)
			}
		}
		if i != 5 && len(inputs) > 0 {
			t.Fatalf("unexpected input in step %d: %v", i, inputs)
		}
		if i == 5 {
			if len(inputs) != 2 || inputs[0]["op"] != "captureTarget" || inputs[1]["op"] != "paste" || inputs[1]["text"] != "https://i.redd.it/meme.png" {
				t.Fatal("Send did not paste only the image link", inputs)
			}
		}
	}
}

// Verify disabling NSFW clears cached image and send source
func TestDisablingNSFWClearsCachedImageAndSendSource(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	preview, width, height, err := thumbnailSizeContext(context.Background(), testImage(t), 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plugin{Engine: e, Pending: map[string]pendingInput{}, Current: map[string]*Meme{"memes": {NSFW: true, URL: "https://i.redd.it/adult.png", Image: preview, Width: width, Height: height}}}
	e.Feature = p.hooks()
	e.Bindings["send"] = engine.Binding{Action: "send", Settings: map[string]any{"shared_id": "memes"}}
	if p.current(e.Bindings["send"]) != nil {
		t.Fatal("NSFW enabled by default")
	}
	e.Global["allow_nsfw"] = true
	p.clearFiltered()
	if p.current(e.Bindings["send"]) == nil {
		t.Fatal("NSFW opt-in ignored")
	}
	all := runSteps(t, e, []step{
		func(ctx context.Context, c *sdk.Client) error { return e.Render(ctx, c, "send") },
		func(ctx context.Context, c *sdk.Client) error {
			if err := e.Handle(ctx, c, sdk.Event{Event: "didReceiveGlobalSettings", Payload: json.RawMessage(`{"settings":{"allow_nsfw":false,"extension":"retained"}}`)}); err != nil {
				return err
			}
			if len(p.Current) != 0 || p.current(e.Bindings["send"]) != nil {
				return fmt.Errorf("disabled NSFW image retained")
			}
			if e.Global["extension"] != "retained" {
				return fmt.Errorf("global extension lost")
			}
			return nil
		},
		func(ctx context.Context, c *sdk.Client) error {
			if err := e.Handle(ctx, c, sdk.Event{Event: "keyDown", Context: "send", Action: "send"}); err == nil {
				return fmt.Errorf("Send accepted an NSFW meme after disable")
			}
			return nil
		},
	})
	cleared := false
	for i, events := range all {
		for _, ev := range events {
			if ev.Event == "desktopInput" {
				t.Fatal("settings change pasted a link")
			}
			if i == 1 && ev.Event == "setImage" {
				cleared = true
			}
		}
	}
	if !cleared {
		t.Fatal("disabling NSFW did not replace displayed image")
	}
}

// Verify copy has no target capture and paste fallback is explicit
func TestCopyHasNoTargetCaptureAndPasteFallbackIsExplicit(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	p := &Plugin{Engine: e, Current: map[string]*Meme{"memes": {URL: "https://i.redd.it/meme.png"}}, Pending: map[string]pendingInput{}}
	e.Feature = p.hooks()
	for _, action := range []string{"copy", "send"} {
		e.Bindings[action] = engine.Binding{Action: action, Settings: map[string]any{"shared_id": "memes"}}
	}
	steps := []step{}
	for _, action := range []string{"copy", "send"} {
		steps = append(steps, func(ctx context.Context, c *sdk.Client) error {
			return e.Handle(ctx, c, sdk.Event{Event: "keyDown", Context: action, Action: action})
		}, func(ctx context.Context, c *sdk.Client) error {
			payload, _ := json.Marshal(map[string]any{"request_id": p.Pending[action].request, "outcome": "Copied to clipboard"})
			if err := e.Handle(ctx, c, sdk.Event{Event: "desktopInputResult", Context: action, Payload: payload}); err != nil {
				return err
			}
			expected := "Link copied"
			if action == "send" {
				expected = "Copied to clipboard; paste unavailable"
			}
			if p.ResultsText[action] != expected {
				return fmt.Errorf("unexpected status: %s", p.ResultsText[action])
			}
			return nil
		})
	}
	events := runSteps(t, e, steps)
	for i, batch := range events {
		ops := []string{}
		for _, event := range batch {
			if event.Event != "desktopInput" {
				continue
			}
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			ops = append(ops, fmt.Sprint(payload["op"]))
			if payload["text"] != nil && payload["text"] != "https://i.redd.it/meme.png" {
				t.Fatal(payload)
			}
		}
		expected := ""
		if i == 0 {
			expected = "copy"
		}
		if i == 2 {
			expected = "captureTarget,paste"
		}
		if strings.Join(ops, ",") != expected {
			t.Fatal(i, ops)
		}
	}
}
