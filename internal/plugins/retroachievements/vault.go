package retroachievements

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
)

// Use a dedicated uninstrumented client because the provider requires query credentials
func newVaultHTTP() *c.HTTP {
	return &c.HTTP{Client: &http.Client{
		Transport:     http.DefaultTransport.(*http.Transport).Clone(),
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Resolve the value and authorization revision of one scoped setup slot
type setupRead func(context.Context, string) (value, revision string, err error)

// Require one current Vault revision for both values before any transfer
func (d *Driver) readSetup(ctx context.Context, s map[string]any, read setupRead) (value any, err error) {
	if d.BaseURL != retroAPI || d.vaultHTTP == nil {
		return nil, fmt.Errorf("managed RetroAchievements credentials require the official HTTPS API")
	}
	defer func() {
		if err != nil {
			d.profile = Profile{}
			d.profileAt = time.Time{}
			d.profileUser = ""
		}
	}()
	user, userRevision, err := read(ctx, "username")
	if err != nil {
		return nil, err
	}
	key, keyRevision, err := read(ctx, "api_key")
	if err != nil {
		return nil, err
	}
	if userRevision == "" || userRevision != keyRevision {
		return nil, &sdk.RPCError{Code: "stale_revision", Message: "RetroAchievements Vault setup changed; retry"}
	}
	if len(key) > 4096 || len(user) > 256 || strings.ContainsAny(key, "\x00\r\n") || strings.ContainsAny(user, "\x00\r\n") {
		return nil, fmt.Errorf("invalid RetroAchievements setup value")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.readAuthorized(ctx, d.vaultHTTP, s, user, key)
}

// Prefer Manager-authorized values once Vault negotiation is present
func (d *Driver) ReadWithClient(ctx context.Context, client *sdk.Client, action string, s map[string]any) (any, error) {
	if !c.Boolean(s, "use_vault_credentials") {
		return d.Read(ctx, s)
	}
	if client == nil || !client.Supports(sdk.VaultValuesProtocol) || !client.Supports(sdk.VaultSecretSharingProtocol) {
		return nil, &sdk.RPCError{Code: "operation_unavailable", Message: "RetroAchievements Vault setup is unavailable"}
	}
	return d.readSetup(ctx, s, func(ctx context.Context, slot string) (string, string, error) {
		selector := sdk.VaultValueRequest{Slot: slot, Field: slot, Action: action}
		if slot == "username" {
			v, err := client.ReadVaultText(ctx, selector)
			return v.Value, v.Revision, err
		}
		v, err := client.ReadVaultSecret(ctx, selector)
		if err != nil {
			return "", "", err
		}
		if v.EntryKind != "api-key" {
			return "", "", &sdk.RPCError{Code: "operation_unavailable", Message: "RetroAchievements needs a Vault API key"}
		}
		return v.Reveal(), v.Revision, nil
	})
}
