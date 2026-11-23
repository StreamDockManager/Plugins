package vaultexample

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fixtureID = "da5c9526-9047-440f-9126-6a12462dea61.66eadeed-b65b-458c-9563-c65252801e9f"

type rpcStep struct {
	method string
	result any
	code   string
	check  func(map[string]any)
}

func exercise(t *testing.T, a string, settings map[string]any, steps ...rpcStep) (string, error) {
	t.Helper()
	info, _ := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-permissions-v1","plugin-http-broker-v1","plugin-vault-values-v1","plugin-vault-secret-sharing-v1","plugin-vault-writes-v1","plugin-vault-references-v1","plugin-text-references-v1"]}}`))
	c, ch := sdk.NewLocalClient(info, "test")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	type outcome struct {
		s   string
		err error
	}
	done := make(chan outcome, 1)
	e := &example{pending: map[string]pending{}}
	go func() {
		s, err := e.execute(ctx, c, sdk.Event{Surface: &sdk.Surface{ActivationToken: "test-press"}, Context: "key"}, engine.Binding{Action: actionID(a), Settings: settings})
		done <- outcome{s, err}
	}()
	for _, step := range steps {
		select {
		case r := <-done:
			t.Fatalf("ended before %s: %v %v", step.method, r.s, r.err)
		case <-ctx.Done():
			t.Fatal("missing request", step.method)
		case req := <-ch:
			if req.Method != step.method {
				t.Fatalf("got %s, wanted %s", req.Method, step.method)
			}
			var p map[string]any
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if step.check != nil {
				step.check(p)
			}
			response := sdk.Event{Event: sdk.RPCResponseEvent, ProtocolID: req.ProtocolID, RequestID: req.RequestID}
			if step.code != "" {
				response.Error = &sdk.RPCError{Code: step.code, Message: "private detail"}
			} else {
				response.Result, _ = json.Marshal(step.result)
			}
			c.ObserveControl(response)
		}
	}
	select {
	case r := <-done:
		return r.s, r.err
	case req := <-ch:
		t.Fatalf("unexpected request %s", req.Method)
	case <-ctx.Done():
		t.Fatal("timeout")
	}
	return "", nil
}

// Verify the opted-in text field only is sent to the host, using the declared read action
func TestDisplayTextUsesTransientPermissionCheckedFormatting(t *testing.T) {
	const raw = "Hello ${vault." + fixtureID + "}"
	settings := map[string]any{"kind": "text", "display_text": raw}
	format := rpcStep{method: "format-text", result: scope{"text": "Hello Ada"}, check: func(p map[string]any) {
		if len(p) != 2 || p["action"] != actionID("read") || p["text"] != raw {
			t.Fatal("unscoped text settings request")
		}
	}}
	if got, err := exercise(t, "read", settings, format); err != nil || got != "Hello Ada" {
		t.Fatalf("unexpected display result: %q, %v", got, err)
	}
	if settings["display_text"] != raw {
		t.Fatal("saved template changed")
	}
	format.code = "permission_denied"
	if got, err := exercise(t, "read", settings, format); err == nil || got != "" {
		t.Fatalf("denied formatting produced text: %q, %v", got, err)
	}
	settings["display_text"] = "Literal display"
	if got, err := exercise(t, "read", settings); err != nil || got != "Literal display" {
		t.Fatalf("literal display requested formatting: %q, %v", got, err)
	}
}

func TestDirectReadsAndRevisionedPrivateWrites(t *testing.T) {
	const value = "fixture-only-value"
	for _, kind := range append([]string{"text"}, secretKinds...) {
		method := "read-secret"
		if kind == "text" {
			method = "read-value"
		}
		check := func(p map[string]any) {
			if p["slot"] != kindField(kind) || p["field"] != kindField(kind) || p["action"] != actionID("read") {
				t.Error("incorrect read scope")
			}
		}
		s, err := exercise(t, "read", map[string]any{"kind": kind}, rpcStep{method: method, result: scope{"entry_kind": kind, "revision": "1", "value": value}, check: check})
		if err != nil || s == "" || strings.Contains(s, value) {
			t.Fatal("unexpected read outcome", err)
		}
		if kind == "text" {
			continue
		}
		for _, rev := range []string{"", "1"} {
			next := "1"
			op := "create"
			if rev != "" {
				next = "2"
				op = "rotate"
			}
			s, err = exercise(t, "write", map[string]any{"kind": kind, "expected_revision": rev}, rpcStep{method: "write-secret", result: scope{"entry_ref": fixtureID, "revision": next}, check: func(p map[string]any) {
				if p["operation"] != op || p["entry_kind"] != kind || p["action"] != actionID("write") || p["field"] != kindField(kind) || p["slot"] != "example_"+kindField(kind) || len(p["value"].(string)) != 64 {
					t.Error("wrong write request")
				}
				if rev != "" && p["expected_revision"] != rev || rev == "" && p["expected_revision"] != nil {
					t.Error("wrong write precondition")
				}
			}})
			if err != nil || s != "Saved revision "+next {
				t.Error("missing write receipt", s, err)
			}
		}
	}
	for _, code := range []string{"needs_setup", "permission_denied", "vault_locked"} {
		_, err := exercise(t, "read", nil, rpcStep{method: "read-value", code: code})
		if err == nil || strings.Contains(err.Error(), "private detail") {
			t.Error("unsafe error", code)
		}
	}
	_, err := exercise(t, "write", nil, rpcStep{method: "write-secret", code: "stale_revision"})
	if err == nil {
		t.Error("lost write conflict")
	}
}

func TestScopedPickerPaginationAndRecipes(t *testing.T) {
	for _, a := range []string{"summary"} {
		yes := rpcStep{method: "has-permission", result: map[string]bool{"granted": true, "available": true}}
		for _, op := range []string{"approve for browsing", "pick for use"} {
			mode := "use-entry"
			if op == "approve for browsing" {
				mode = "browse-metadata"
			}
			s, err := exercise(t, a, map[string]any{"operation": op}, yes, rpcStep{method: "select-vault-entry", result: scope{"review_id": "pick", "state": "pending"}, check: func(p map[string]any) {
				if p["scope"].(map[string]any)["mode"] != mode || p["activation_token"] != "test-press" {
					t.Error("wrong selection scope")
				}
			}})
			if err != nil || s != "Choose in Manager" {
				t.Fatal(s, err)
			}
		}
		s, err := exercise(t, a, map[string]any{"operation": "list approved names"}, yes, rpcStep{method: "list-vault-entries", result: map[string]any{"entries": []any{scope{"name": "First", "id": fixtureID}}, "next": 1}}, rpcStep{method: "list-vault-entries", result: map[string]any{"entries": []any{scope{"name": "Second", "id": fixtureID}}}, check: func(p map[string]any) {
			if p["start"] != float64(1) {
				t.Error("wrong cursor")
			}
		}})
		if err != nil || s != "2 approved names" {
			t.Error(s, err)
		}
		for _, op := range []string{"use selection", "use setup slot"} {
			steps := make([]rpcStep, len(summaryScopes()))
			for i := range steps {
				steps[i] = yes
			}
			ref := fixtureID
			field := summaryAccess("use-entry")["field"]
			if op == "use setup slot" {
				ref = "slot:location"
				if a == "summary" {
					ref = "slot:summary_bearer_token"
					field = "bearer_authorization"
				}
			}
			steps = append(steps, rpcStep{method: summaryAccess("use-entry")["recipe"], result: scope{"state": "pending", "result_handle": "handle"}, check: func(p map[string]any) {
				if p["entry_ref"] != ref || p["scope"].(map[string]any)["field"] != field {
					t.Error("wrong broker scope")
				}
			}})
			s, err = exercise(t, a, map[string]any{"operation": op, "reference": "${vault." + fixtureID + "}", "kind": "bearer-token"}, steps...)
			if err != nil || s != "Review in Manager" {
				t.Fatal(s, err)
			}
		}
	}
}

func TestNetworkGrantsAreIndependentByTransportAndSender(t *testing.T) {
	for _, sender := range []string{"plugin", "manager"} {
		for _, transport := range []string{"http", "https"} {
			want := network(sender, transport)
			check := func(p map[string]any) {
				v, _ := json.Marshal(p["scope"])
				var actual scope
				json.Unmarshal(v, &actual)
				if !reflect.DeepEqual(actual, want) {
					t.Fatal("wrong scope", v)
				}
			}
			steps := []rpcStep{{method: "has-permission", result: map[string]bool{"available": true}, check: check}}
			if sender == "manager" {
				steps = append(steps, rpcStep{method: "has-permission", result: map[string]bool{"available": true, "granted": true}})
			}
			steps = append(steps, rpcStep{method: "request-permissions", result: scope{"state": "pending", "review_id": "grant"}})
			s, err := exercise(t, sender+"-post", map[string]any{"transport": transport}, steps...)
			if err != nil || s != "Review permissions" {
				t.Fatal(s, err)
			}
		}
	}
	s, err := exercise(t, "permissions", nil, rpcStep{method: "get-permissions", result: map[string]any{"scopes": []any{map[string]bool{"granted": true}, map[string]bool{"granted": false}}}})
	if s != "1 of 2 granted" || err != nil {
		t.Fatal(s, err)
	}
}

// A late Manager response must never update a different binding or expose an entry's display name.
func TestSelectionNoticeStoresOnlyTheReviewedReference(t *testing.T) {
	ctx := context.Background()
	info, _ := sdk.ParseHostInfo([]byte("{}"))
	c, out := sdk.NewLocalClient(info, "session")
	base := authoring.New(Definition, "", nil)
	id := actionID("summary")
	base.Bindings["key"] = engine.Binding{Action: id, Settings: map[string]any{"operation": "pick for use", "other": "keep"}}
	e := &example{base: base, status: map[string]string{}, pending: map[string]pending{"current": {binding: "key", action: id, operation: "pick for use", expires: time.Now().Add(time.Minute)}}}
	notice := func(review string) sdk.Event {
		data, _ := json.Marshal(map[string]any{"event": "streamDockManagerVaultSelectionResult", "review_id": review, "outcome": "selected", "entry": scope{"id": fixtureID, "name": "private display name"}})
		return sdk.Event{Event: "streamDockManagerVaultSelectionResult", Raw: data}
	}
	// An SDK render hook isolates the picker persistence assertion from layout messages.
	base.Feature.Render = func(context.Context, *sdk.Client, string) error { return nil }
	if _, err := e.notice(ctx, c, notice("stale")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out:
		t.Fatal("stale selection was saved")
	default:
	}
	if _, err := e.notice(ctx, c, notice("current")); err != nil {
		t.Fatal(err)
	}
	msg := <-out
	var settings scope
	if err := json.Unmarshal(msg.Payload, &settings); err != nil {
		t.Fatal(err)
	}
	if msg.Event != "setSettings" || msg.Context != "key" || !reflect.DeepEqual(settings, scope{"reference": "${vault." + fixtureID + "}", "operation": "use selection", "other": "keep"}) {
		t.Fatal("incorrect selection persistence")
	}
	if len(e.pending) != 0 || base.Bindings["key"].Settings["reference"] != nil {
		t.Fatal("mutated binding settings")
	}
	if _, err := e.notice(ctx, c, notice("current")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out:
		t.Fatal("selection was replayed")
	default:
	}
}
