package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// TestInitialThemeUsesSDKSnapshotWithoutDuplicateRPC verifies initial theme uses SDK snapshot without duplicate RPC
func TestInitialThemeUsesSDKSnapshotWithoutDuplicateRPC(t *testing.T) {
	for _, initial := range []string{"light", "dark", "absent", "failed"} {
		t.Run(initial, func(t *testing.T) {
			hostJSON := `{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1","plugin-theme-v1"],"theme_rpc":true}}`
			if initial == "absent" {
				hostJSON = `{}`
			}
			host, err := sdk.ParseHostInfo([]byte(hostJSON))
			if err != nil {
				t.Fatal(err)
			}
			c, outgoing := sdk.NewLocalClient(host, "session")
			var requests atomic.Int32
			done := make(chan struct{})
			defer close(done)
			go func() {
				for {
					select {
					case request := <-outgoing:
						if request.Event != sdk.RPCRequestEvent {
							continue
						}
						requests.Add(1)
						response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: request.ProtocolID, RequestID: request.RequestID}
						if initial == "failed" {
							response.Error = &sdk.RPCError{Code: "unavailable", Message: "fixture failure"}
						} else {
							response.Result = json.RawMessage(fmt.Sprintf(`{"version":1,"resolved":%q,"revision":1,"theme":{"surface":"#172138","ink":"#ffffff","muted":"#aaaaaa","accent":"#ff0000","meter":"#00ff00","border":"#111111","wallpaper":"<svg/>"}}`, initial))
						}
						c.ObserveControl(response)
					case <-done:
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// Emulate the SDK's transport bootstrap before delivering the first event
			if initial != "absent" {
				_, err = c.GetPluginTheme(ctx)
				if (err != nil) != (initial == "failed") {
					t.Fatal(err)
				}
			}
			h := &managedHandler{Engine: New(Definition{Slug: "theme-test"}, "", func(*Engine) Feature { return Feature{} })}
			defer h.Close()
			if err := h.Handle(ctx, c, sdk.Event{Event: "fixture"}); err != nil {
				t.Fatal(err)
			}
			want := initial
			if initial == "absent" || initial == "failed" {
				want = ""
			}
			if h.Theme != want {
				t.Fatalf("initial theme %q, want %q", h.Theme, want)
			}
			next := "light"
			if want == "light" {
				next = "dark"
			}
			if err := h.Handle(ctx, c, sdk.Message(sdk.ThemeEvent, "", map[string]any{"resolved": next})); err != nil {
				t.Fatal(err)
			}
			if err := h.Handle(ctx, c, sdk.Event{Event: "fixture"}); err != nil {
				t.Fatal(err)
			}
			if h.Theme != next {
				t.Fatal("later theme overwritten by initial snapshot")
			}
			expectedRequests := int32(1)
			if initial == "absent" {
				expectedRequests = 0
			}
			if requests.Load() != expectedRequests {
				t.Fatalf("duplicate theme RPC: %d", requests.Load())
			}
		})
	}
}
