package vaultexample

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type pending struct {
	binding, action, operation string
	expires                    time.Time
}
type example struct {
	base    *engine.Engine
	status  map[string]string
	pending map[string]pending
	server  *webhookServer
}

var referenceID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func Factory(base *engine.Engine) engine.Feature {
	e := &example{base: base, status: map[string]string{}, pending: map[string]pending{}}
	visuals := map[string]engine.Visual{}
	for _, a := range Definition.Actions {
		visuals[a] = VisualFor(a)
	}
	return engine.Feature{Inspector: base.InspectBasic, Visuals: visuals, Action: e.activate, Before: e.notice, Status: func(_ context.Context, id string, b engine.Binding) engine.DisplayStatus {
		a := engine.Action(b.Action)
		s := e.status[id]
		if s == "" {
			s = ActionName(a)
		}
		return engine.DisplayStatus{Text: s, Icon: VisualFor(a).Icon}
	}, Unbound: e.forget, Settings: e.forget, Close: func() {
		if e.server != nil {
			e.server.close()
		}
	}}
}
func (e *example) forget(id string) {
	delete(e.status, id)
	for k, v := range e.pending {
		if v.binding == id {
			delete(e.pending, k)
		}
	}
}
func (e *example) notice(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	if ev.Event == "streamDockManagerPermissionsChanged" {
		clear(e.pending)
		clear(e.status)
		for id := range e.base.Bindings {
			if err := e.base.Render(ctx, c, id); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	for id, p := range e.pending {
		if time.Now().After(p.expires) {
			delete(e.pending, id)
		}
	}
	n, ok := ev.PermissionNotice()
	if !ok {
		return false, nil
	}
	id := n.ReviewID
	p, ok := e.pending[id]
	if !ok {
		return true, nil
	}
	delete(e.pending, id)
	b, ok := e.base.Bindings[p.binding]
	if !ok || b.Action != p.action {
		return true, nil
	}
	switch n.Event {
	case "streamDockManagerVaultSelectionResult":
		if n.Outcome != "selected" || n.Entry == nil || !referenceID.MatchString(n.Entry.ID) {
			e.status[p.binding] = "Selection closed"
			break
		}
		if p.operation == "pick for use" {
			s := engine.CloneSettings(b.Settings)
			s["reference"] = "${vault." + n.Entry.ID + "}"
			s["operation"] = "use selection"
			if err := c.Settings(ctx, p.binding, s); err != nil {
				return true, err
			}
		}
		e.status[p.binding] = "Entry approved"
	}
	return true, e.base.Render(ctx, c, p.binding)
}
func setting(s map[string]any, key, fallback string) string {
	if v, ok := s[key].(string); ok {
		return v
	}
	return fallback
}
func (e *example) activate(ctx context.Context, c *sdk.Client, ev sdk.Event, b engine.Binding) error {
	if !engine.Active(ev) {
		return nil
	}
	status, err := e.execute(ctx, c, ev, b)
	if err != nil {
		var unsupported *sdk.UnsupportedFeatureError
		var rpc *sdk.RPCError
		if errors.As(err, &rpc) {
			status = strings.ReplaceAll(rpc.Code, "_", " ")
		} else if errors.As(err, &unsupported) {
			status = "Review setup in Manager"
		} else {
			status = "Operation unavailable"
		}
	}
	e.status[ev.Context] = status
	return e.base.Render(ctx, c, ev.Context)
}
func (e *example) execute(ctx context.Context, c *sdk.Client, ev sdk.Event, b engine.Binding) (string, error) {
	a := engine.Action(b.Action)
	switch a {
	case "location":
		_, err := c.ReadVaultLocation(ctx, sdk.VaultValueRequest{Action: b.Action, Slot: "location", Field: "location"})
		if err != nil {
			return "", err
		}
		return "Location read", nil
	case "permissions":
		permissions, err := c.GetPermissions(ctx)
		if err != nil {
			return "", err
		}
		granted := 0
		for _, p := range permissions.Scopes {
			if p.Granted {
				granted++
			}
		}
		return fmt.Sprintf("%d of %d granted", granted, len(permissions.Scopes)), nil
	case "read", "write":
		kind := setting(b.Settings, "kind", "api-key")
		if a == "read" {
			kind = setting(b.Settings, "kind", "text")
		}
		field := kindField(kind)
		if field == "" {
			return "Choose a supported kind", nil
		}
		sel := sdk.VaultValueRequest{Slot: field, Field: field, Action: b.Action}
		if a == "read" {
			if kind == "text" {
				if setting(b.Settings, "display_text", "") != "" {
					return engine.FormatSettingText(ctx, c, b, "display_text", "")
				}
				v, err := c.ReadVaultText(ctx, sel)
				if err != nil {
					return "", err
				}
				if v.Value == "" {
					return "Empty text read", nil
				}
				return "Text read", nil
			}
			v, err := c.ReadVaultSecret(ctx, sel)
			if err != nil {
				return "", err
			}
			if v.EntryKind != kind || v.Reveal() == "" {
				return "Unexpected credential", nil
			}
			return "Credential read", nil
		}
		if kind == "text" {
			return "Choose a secret kind", nil
		}
		sel.Slot = "example_" + field
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		receipt, err := c.WriteVaultSecret(ctx, sel, kind, hex.EncodeToString(bytes), setting(b.Settings, "expected_revision", ""))
		if err != nil {
			return "", err
		}
		return "Saved revision " + receipt.Revision, nil
	case "webhooks":
		if err := e.listen(); err != nil {
			return "Viewer ports in use", nil
		}
		return "Viewer\n127.0.0.1:39480", nil
	}
	if ev.Surface == nil || ev.Surface.ActivationToken == "" {
		return "Press the dock control", nil
	}
	token := ev.Surface.ActivationToken
	if a == "plugin-post" || a == "manager-post" {
		sender := strings.TrimSuffix(a, "-post")
		transport := setting(b.Settings, "transport", "https")
		if transport != "http" && transport != "https" {
			return "Choose HTTP or HTTPS", nil
		}
		scopes := []scope{network(sender, transport)}
		if sender == "manager" {
			scopes = append(scopes, scope{"id": "ui.popup", "mode": "text-result"})
		}
		if ready, err := grants(ctx, c, b.Action, token, scopes); !ready || err != nil {
			return "Review permissions", err
		}
		if err := e.listen(); err != nil {
			return "Viewer ports in use", nil
		}
		if sender == "plugin" {
			return "Ping delivered", e.server.post(ctx, transport)
		}
		if transport == "http" {
			_, err := c.SendLocalWebhook(ctx, token, scopes[0]["origin"])
			return "Review ping in Manager", err
		}
		_, err := c.SendExampleWebhook(ctx, token, e.server.cert)
		return "Review ping in Manager", err
	}
	if a != "summary" {
		return "Unknown action", nil
	}
	op := setting(b.Settings, "operation", "pick for use")
	s := summaryAccess("use-entry")
	scopes := []scope{s}
	switch op {
	case "approve for browsing", "list approved names":
		s = summaryAccess("browse-metadata")
		scopes = []scope{s}
	case "use setup slot", "use selection":
		scopes = summaryScopes()
		s = scopes[0]
		if op == "use setup slot" && setting(b.Settings, "kind", "api-key") == "bearer-token" {
			s["field"] = "bearer_authorization"
		}
	case "pick for use":
	default:
		return "Choose a Vault operation", nil
	}
	if ready, err := grants(ctx, c, b.Action, token, scopes); !ready || err != nil {
		return "Review permissions", err
	}
	if op == "list approved names" {
		count := 0
		for start := 0; start < 4096; {
			page, err := c.ListVaultEntries(ctx, b.Action, s, start)
			if err != nil {
				return "", err
			}
			count += len(page.Entries)
			if page.Next == nil {
				return fmt.Sprintf("%d approved names", count), nil
			}
			if *page.Next <= start {
				break
			}
			start = *page.Next
		}
		return "Invalid Vault page", nil
	}
	p := pending{ev.Context, b.Action, op, time.Now().Add(90 * time.Second)}
	if len(e.pending) >= 8 {
		return "Finish an open review", nil
	}
	if op == "pick for use" || op == "approve for browsing" {
		r, err := c.SelectVaultEntry(ctx, token, b.Action, s)
		if err != nil {
			return "", err
		}
		e.pending[r.ReviewID] = p
		return "Choose in Manager", nil
	}
	ref := "slot:summary_" + kindField(setting(b.Settings, "kind", "api-key"))
	if op == "use selection" {
		v := setting(b.Settings, "reference", "")
		ref = strings.TrimSuffix(strings.TrimPrefix(v, "${vault."), "}")
		if v != "${vault."+ref+"}" || !referenceID.MatchString(ref) {
			return "Choose an entry first", nil
		}
	}
	_, err := c.RunVaultRecipe(ctx, token, s, ref)
	return "Review in Manager", err
}
func grants(ctx context.Context, c *sdk.Client, action, token string, scopes []scope) (bool, error) {
	var missing []scope
	for _, s := range scopes {
		g, err := c.HasPermission(ctx, action, s)
		if err != nil {
			return false, err
		}
		if !g.Available {
			return false, &sdk.RPCError{Code: "operation_unavailable"}
		}
		if !g.Granted {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return true, nil
	}
	_, err := c.RequestPermissions(ctx, token, missing)
	return false, err
}
func (e *example) listen() error {
	if e.server != nil {
		return nil
	}
	s, err := startWebhookServer(webhookAddress, viewerAddress)
	e.server = s
	return err
}
