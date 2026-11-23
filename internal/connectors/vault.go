package connectors

import (
	"context"
	"errors"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	services "github.com/StreamDockManager/Plugin-SDK/Go/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

type preparedKey struct{}

// VaultSession fences the driver's reusable connection and caches when its
// authorized action or Vault revision changes. Raw values live only in a call.
type VaultSession struct {
	mu       sync.Mutex
	identity string
}

func (v *VaultSession) adopt(identity string, close func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.identity != identity {
		close()
		v.identity = identity
	}
}

func Factory(options Options) engine.Factory {
	options.UsesVault = len(metadata.VaultFields(options.Definition.Slug, "")) > 0 || options.Definition.Slug == "retroachievements"
	return services.Factory(options)
}

// Separate polling samples authorized for different actions.
func VaultKey(s map[string]any, slug string) string {
	action := String(s, "_action", "")
	var fields []string
	for _, field := range metadata.VaultFields(slug, action) {
		if Boolean(s, "use_vault_"+field.Field) {
			fields = append(fields, field.Field)
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return "vault:" + action + ":" + strings.Join(fields, ",") + ":"

}

func WithVault[T any](ctx context.Context, s map[string]any, slug string, session *VaultSession, close func(), run func(context.Context, map[string]any) (T, error)) (T, error) {
	if ctx.Value(preparedKey{}) != nil {
		return run(ctx, s)
	}
	action := String(s, "_action", "")
	identity := VaultKey(s, slug)
	if identity == "" {
		session.adopt("", close)
		return run(ctx, s)
	}
	var zero T
	client := services.HostClient(ctx)
	if client == nil || action == "" {
		return zero, &sdk.RPCError{Code: "operation_unavailable", Message: "Choose an action with an approved Vault entry"}
	}
	settings := maps.Clone(s)
	revision := ""
	for _, field := range metadata.VaultFields(slug, action) {
		if !Boolean(s, "use_vault_"+field.Field) {
			continue
		}
		selector := sdk.VaultValueRequest{Slot: field.Field, Field: field.Field, Action: engine.ID(slug) + "." + action}
		var next string
		if field.EntryKind == "text" {
			value, err := client.ReadVaultText(ctx, selector)
			if err != nil {
				session.adopt("", close)
				return zero, err
			}
			settings[field.Field] = value.Value
			next = value.Revision
		} else {
			value, err := client.ReadVaultSecret(ctx, selector)
			if err != nil {
				session.adopt("", close)
				return zero, err
			}
			if value.EntryKind != field.EntryKind || strings.ContainsRune(value.Reveal(), 0) {
				session.adopt("", close)
				return zero, &sdk.RPCError{Code: "operation_unavailable", Message: "Invalid Vault credential"}
			}
			settings[field.Field] = value
			next = value.Revision
		}
		if next == "" || (revision != "" && revision != next) {
			session.adopt("", close)
			return zero, &sdk.RPCError{Code: "stale_revision", Message: "Vault settings changed. Retry the action."}
		}
		revision = next
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	session.adopt(identity+revision, close)
	value, err := run(context.WithValue(ctx, preparedKey{}, true), settings)
	if err != nil {
		return zero, errors.New("Service request using Vault failed")
	}
	return value, nil
}

func Secret(s map[string]any, key string) (string, error) {
	if v, ok := s[key].(sdk.VaultSecret); ok {
		return v.Reveal(), nil
	}
	if v, ok := s[key].(string); ok && Boolean(s, "use_vault_"+key) && key == "username" {
		return v, nil
	}
	return services.Secret(s, key)
}

// Managed requests use an uninstrumented transport so credentials and provider
// responses never enter the SDK's optional network diagnostics.
var privateServiceHTTP = &HTTP{Client: &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}}

func ServiceHTTP(ctx context.Context, ordinary *HTTP) *HTTP {
	if ctx.Value(preparedKey{}) != nil {
		return privateServiceHTTP
	}
	return ordinary
}
