package retroachievements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
)

type setupTransport func(*http.Request) (*http.Response, error)

// Intercept synthetic managed reads without making a network connection
func (f setupTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Verify managed values stay out of settings and cannot route to another service
func TestManagedSetupUsesFixedServiceWithoutSettingsMutation(t *testing.T) {
	const secret = "synthetic-vault-key"
	var calls int
	d := &Driver{HTTP: c.NewHTTP(), vaultHTTP: newVaultHTTP(), BaseURL: retroAPI, Now: time.Now}
	defer d.Close()
	d.vaultHTTP.Client.Transport = setupTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "retroachievements.org" || req.URL.Path != "/API/API_GetUserProfile.php" || req.URL.Query().Get("y") != secret || req.URL.Query().Get("u") != "managed-user" {
			t.Error("unexpected managed request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"User":"managed-user","TotalPoints":10,"TotalSoftcorePoints":20}`)), Header: make(http.Header)}, nil
	})
	settings := map[string]any{"username": "old-user", "api_key": "old-key"}
	read := func(_ context.Context, slot string) (string, string, error) {
		if slot == "username" {
			return "managed-user", "3", nil
		}
		return secret, "3", nil
	}
	if _, err := d.readSetup(context.Background(), settings, read); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || settings["username"] != "old-user" || settings["api_key"] != "old-key" || len(settings) != 2 {
		t.Fatal("settings mutated or wrong transfer count")
	}
	d.BaseURL = "https://elsewhere.invalid/API/"
	if _, err := d.readSetup(context.Background(), settings, read); err == nil || calls != 1 {
		t.Fatal("arbitrary destination allowed")
	}
}

// Verify revoked setup cannot use old settings or reuse a previously loaded sample
func TestManagedSetupDenialSendsNothingAndRejectsReflectedSecrets(t *testing.T) {
	const secret = "synthetic-vault-key"
	var calls int
	d := &Driver{HTTP: c.NewHTTP(), vaultHTTP: newVaultHTTP(), BaseURL: retroAPI, Now: time.Now}
	defer d.Close()
	d.vaultHTTP.Client.Transport = setupTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"User":%q,"TotalPoints":10,"TotalSoftcorePoints":20}`, secret))), Header: make(http.Header)}, nil
	})
	settings := map[string]any{"username": "legacy-user", "api_key": "legacy-key"}
	denied := errors.New("Vault denied")
	if _, err := d.readSetup(context.Background(), settings, func(_ context.Context, slot string) (string, string, error) {
		if slot == "username" {
			return "managed-user", "3", nil
		}
		return "", "", denied
	}); !errors.Is(err, denied) || calls != 0 {
		t.Fatal("denial fell back to old settings")
	}
	if value, err := d.readSetup(context.Background(), settings, func(_ context.Context, slot string) (string, string, error) {
		if slot == "username" {
			return "managed-user", "3", nil
		}
		return secret, "3", nil
	}); value != nil || err == nil || strings.Contains(err.Error(), secret) || calls != 1 {
		t.Fatal("reflected response was not discarded safely")
	}
}

// Verify the real SDK envelope, scoped action and revision mismatch denial
func TestManagedSDKReadUsesExactSlotsAndRejectsMixedRevision(t *testing.T) {
	const action = "app.streamdockmanager.plugins.retroachievements.completion"
	const secret = "synthetic-sdk-key"
	info, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-vault-values-v1","plugin-vault-secret-sharing-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, revision string
		calls          int
	}{{"approved", "3", 2}, {"changed", "4", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			client, outgoing := sdk.NewLocalClient(info, "fixture")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				for _, want := range []struct{ slot, protocol, method, kind, value, revision string }{
					{"username", sdk.VaultValuesProtocol, "read-value", "text", "vault-user", "3"},
					{"api_key", sdk.VaultSecretSharingProtocol, "read-secret", "api-key", secret, tc.revision},
				} {
					select {
					case req := <-outgoing:
						var got sdk.VaultValueRequest
						if json.Unmarshal(req.Payload, &got) != nil || got.Action != action || got.Slot != want.slot || got.Field != want.slot || req.ProtocolID != want.protocol || req.Method != want.method {
							t.Error("unexpected Vault selector")
						}
						result, _ := json.Marshal(map[string]string{"entry_kind": want.kind, "value": want.value, "revision": want.revision})
						client.ObserveControl(sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID, Result: result})
					case <-ctx.Done():
						t.Error("missing Vault request")
						return
					}
				}
			}()
			calls := 0
			d := &Driver{HTTP: c.NewHTTP(), vaultHTTP: newVaultHTTP(), BaseURL: retroAPI, Now: time.Now}
			defer d.Close()
			d.vaultHTTP.Client.Transport = setupTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Query().Get("y") != secret || req.URL.Query().Get("u") != "vault-user" {
					t.Error("managed selection not applied")
				}
				body := `{"User":"vault-user","LastGameID":12,"TotalPoints":10,"TotalSoftcorePoints":20}`
				if calls == 2 {
					body = `{"ID":12,"Title":"Game","NumAchievements":5,"NumAwardedToUser":2,"NumAwardedToUserHardcore":1}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			result, err := d.ReadWithClient(ctx, client, action, map[string]any{"use_vault_credentials": true, "_action": "completion", "username": "portable", "api_key": "portable-key"})
			<-done
			if calls != tc.calls {
				t.Errorf("got %d outward requests; want %d", calls, tc.calls)
			}
			if calls > 0 {
				if err != nil || result == nil {
					t.Fatal("approved read failed", err)
				}
			} else {
				var rpc *sdk.RPCError
				if !errors.As(err, &rpc) || rpc.Code != "stale_revision" || result != nil {
					t.Fatal("mixed revision accepted", err)
				}
			}
		})
	}
}
