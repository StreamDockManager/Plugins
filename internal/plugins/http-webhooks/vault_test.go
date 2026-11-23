package httpwebhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

func TestVaultWebhookUsesReviewedURLAndStopsOnRevocation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/private-fixture" || r.Method != "POST" || string(data) != "ping" {
			t.Error("wrong webhook request")
		}
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "private-fixture")
	}))
	defer server.Close()
	info, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-vault-secret-sharing-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	client, messages := sdk.NewLocalClient(info, "webhook-fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	action := engine.ID(Definition.Slug) + ".request"
	settings := map[string]any{"url": server.URL + "/old-url", "use_vault_webhook": true, "method": "POST", "body": "ping"}
	for _, approved := range []bool{true, false} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			select {
			case req := <-messages:
				var slot sdk.VaultValueRequest
				if json.Unmarshal(req.Payload, &slot) != nil || slot.Action != action || slot.Field != "webhook" || slot.Slot != "webhook" || req.Method != "read-secret" {
					t.Error("incorrect Vault request")
				}
				response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID}
				if approved {
					response.Result, _ = json.Marshal(map[string]string{"entry_kind": "credential", "revision": "1", "value": server.URL + "/private-fixture"})
				} else {
					response.Error = &sdk.RPCError{Code: "permission_denied", Message: "fixture"}
				}
				client.ObserveControl(response)
			case <-ctx.Done():
				t.Error("missing scoped Vault request")
			}
		}()
		result, err := vaultWebhook(ctx, client, action, settings, false)
		<-done
		if approved && (err != nil || result.Status != 202) || !approved && err == nil {
			t.Fatalf("approval %v result %d error %v", approved, result.Status, err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "private-fixture") || calls.Load() != 1 || settings["url"] != server.URL+"/old-url" {
			t.Fatal("disclosed private URL, retained response or used fallback")
		}
	}
	if _, err := vaultWebhook(ctx, nil, action, settings, false); err == nil {
		t.Fatal("unavailable host used ordinary settings")
	}
}
