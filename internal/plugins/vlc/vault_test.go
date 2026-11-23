package vlc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
)

func TestConnectorPollResolvesVaultAndClearsRevokedSample(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || password != "fixture-credential" {
			t.Error("used ordinary password for managed read")
		}
		calls.Add(1)
		_, _ = io.WriteString(w, `{"state":"playing","volume":120,"time":10,"length":50}`)
	}))
	defer server.Close()
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-vault-secret-sharing-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	client, messages := sdk.NewLocalClient(host, "fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := fixtureengine.New(Definition, filepath.Join(t.TempDir(), "state.json"), Factory)
	defer e.Close()
	id := engine.ID(Definition.Slug) + ".volume-display"
	s := map[string]any{"use_vault_password": true}
	e.Bindings["key"] = engine.Binding{Action: id, Settings: s}
	e.Global = map[string]any{"endpoint": server.URL, "password": "ordinary"}
	e.Feature.Render = func(context.Context, *sdk.Client, string) error { return nil }
	e.Feature.DemandChanged()
	for _, approved := range []bool{true, false} {
		if !approved {
			_, err = e.Feature.Before(ctx, client, sdk.Event{Event: "streamDockManagerPermissionsChanged"})
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = e.Feature.Poll(ctx, client); err != nil {
			t.Fatal(err)
		}
		select {
		case req := <-messages:
			var got sdk.VaultValueRequest
			if json.Unmarshal(req.Payload, &got) != nil || req.Method != "read-secret" || got.Action != id || got.Field != "password" {
				t.Fatal("missing action-scoped read")
			}
			response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID}
			if approved {
				response.Result = []byte(`{"value":"fixture-credential","revision":"1","entry_kind":"credential"}`)
			} else {
				response.Error = &sdk.RPCError{Code: "permission_denied", Message: "fixture"}
			}
			client.ObserveControl(response)
		case <-ctx.Done():
			t.Fatal("connector did not request its grant")
		}
		for len(e.Jobs) > 0 && ctx.Err() == nil {
			if err = e.DrainJobs(ctx, client); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		shown := e.Feature.Status(ctx, "key", e.Bindings["key"]).Text
		if calls.Load() != 1 || strings.Contains(shown, "47%") != approved || e.Global["password"] != "ordinary" || len(s) != 1 {
			t.Fatal("managed poll retained or published incorrect state", shown)
		}
	}
}
