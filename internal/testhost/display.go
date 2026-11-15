package testhost

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// DriveDisplays provides the mock transport only. Each plugin owns its steps,
// lifecycle driver and assertions, including specialized device simulations
func DriveDisplays(t *testing.T, count int, handler sdk.Handler) [][]sdk.Event {
	t.Helper()
	results := make(chan [][]sdk.Event, 1)
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
		if err := wsjson.Read(wsCtx, conn, &registration); err != nil || registration.UUID != "display-fixture" {
			t.Error(registration, err)
			return
		}
		all := make([][]sdk.Event, count)
		for i := range count {
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
						t.Error("display update failed", string(event.Payload))
					}
					break
				}
				all[i] = append(all[i], event)
			}
		}
		results <- all
		_ = wsjson.Write(wsCtx, conn, sdk.Event{Event: "shutdown"})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := Connect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "display-fixture", handler); err != nil {
		t.Fatal(err)
	}
	select {
	case all := <-results:
		return all
	case <-ctx.Done():
		t.Fatal("display fixture timed out")
		return nil
	}
}

// DisplayEvent constructs a host display fixture event
func DisplayEvent(kind, id, action string) sdk.Event {
	width := 112
	if id == "touch" {
		width = 176
	}
	return sdk.Event{Event: kind, Context: id, Action: action, Payload: json.RawMessage(`{"settings":{"seconds":10}}`), Surface: &sdk.Surface{Width: width, Height: 112}}
}

// VisualPayload extracts the visual payload from recorded host output
func VisualPayload(events []sdk.Event, id, kind string) map[string]any {
	for _, event := range slices.Backward(events) {

		if event.Context == id && event.Event == kind {
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			return payload
		}
	}
	return nil
}
