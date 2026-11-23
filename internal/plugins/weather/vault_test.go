package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

func TestSharedLocationUsesWeatherClientForEveryAction(t *testing.T) {
	requests := make(chan string, 4)
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("latitude") != "12.34567" || q.Get("longitude") != "0" || q.Get("temperature_unit") != "fahrenheit" {
			t.Error("incorrect coordinates or units")
		}
		requests <- r.URL.Path
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":45,"weather_code":0},"daily":{"temperature_2m_max":[50],"temperature_2m_min":[40],"sunrise":["2026-11-20T07:15"],"sunset":["2026-11-20T16:40"]}}`))
	}))
	defer provider.Close()
	info, _ := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-vault-values-v1"]}}`))
	client, events := sdk.NewLocalClient(info, "weather")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := map[string]any{"use_vault_location": true, "endpoint": provider.URL + "/forecast", "latitude": 50.0, "longitude": 60.0, "units": "fahrenheit"}
	for a, expected := range map[string]string{"conditions": "45.0 °F", "forecast": "40.0 °F\n50.0 °F", "sun": "07:15\n16:40"} {
		uuid := "app.streamdockmanager.plugins.weather." + a
		r := statusRequest{binding: binding{Action: uuid, Settings: s}, vaultClient: client, weatherClient: provider.Client()}
		done := make(chan probeStatus, 1)
		go func() { done <- r.sample(ctx) }()
		select {
		case req := <-events:
			var selector sdk.VaultValueRequest
			if json.Unmarshal(req.Payload, &selector) != nil || selector != (sdk.VaultValueRequest{Action: uuid, Field: "location", Slot: "location"}) || req.Method != "read-location" || req.ProtocolID != sdk.VaultValuesProtocol {
				t.Fatal("unexpected host request")
			}
			client.ObserveControl(sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID, Result: json.RawMessage(`{"entry_kind":"location","revision":"3","value":{"latitude":12.34567,"longitude":0}}`)})
		case <-ctx.Done():
			t.Fatal("missing location read")
		}
		result := <-done
		if result.Status.Unavailable || result.Status.Text != expected || result.Weather != nil {
			t.Fatal("incorrect private forecast", a, result.Status.Text)
		}
		select {
		case path := <-requests:
			if path != "/forecast" {
				t.Fatal("wrong Weather endpoint")
			}
		default:
			t.Fatal("no request reached Weather's provider")
		}
	}
	revoked := statusRequest{binding: binding{Action: "app.streamdockmanager.plugins.weather.conditions", Settings: s}, vaultClient: client, weatherClient: provider.Client()}
	done := make(chan probeStatus, 1)
	go func() { done <- revoked.sample(ctx) }()
	select {
	case req := <-events:
		client.ObserveControl(sdk.Event{Event: sdk.RPCResponseEvent, RequestID: req.RequestID, ProtocolID: req.ProtocolID, Error: &sdk.RPCError{Code: "needs_setup"}})
	case <-ctx.Done():
		t.Fatal("revoked binding did not recheck its grant")
	}
	if result := <-done; !result.Status.Unavailable || result.Weather != nil || len(requests) != 0 {
		t.Fatal("revoked binding used stale coordinates")
	}
	r := statusRequest{binding: binding{Action: "fixture.conditions", Settings: s}, weatherClient: provider.Client()}
	if !r.sample(ctx).Status.Unavailable || len(requests) != 0 || s["latitude"] != 50.0 {
		t.Fatal("missing grant used portable coordinates or changed settings")
	}
}
