package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// A host receives only the selected text and its fully qualified action, not the binding
func TestFormatSettingTextIsExplicitAndTransient(t *testing.T) {
	saved := map[string]any{"body": "Hi ${vault.alias}", "url": "https://${vault.origin}", "n": 3}
	binding := Binding{Action: "app.streamdockmanager.plugins.example.show", Settings: saved}
	calls := 0
	format := func(_ context.Context, action, text string) (string, error) {
		calls++
		if action != binding.Action || text != "Hi ${vault.alias}" {
			t.Fatalf("unexpected request: %s %s", action, text)
		}
		return "Hi Ada", nil
	}
	value, err := formatSettingText(t.Context(), format, binding, "body", "")
	if err != nil || value != "Hi Ada" || calls != 1 {
		t.Fatalf("unexpected format result: %q %v (%d calls)", value, err, calls)
	}
	if !reflect.DeepEqual(saved, map[string]any{"body": "Hi ${vault.alias}", "url": "https://${vault.origin}", "n": 3}) {
		t.Fatal("formatted text mutated settings")
	}
	for field, want := range map[string]string{"missing": "fallback", "n": "fallback", "literal": "${ENV}"} {
		saved["literal"] = "${ENV}"
		value, err := formatSettingText(t.Context(), format, binding, field, "fallback")
		if err != nil || value != want || calls != 1 {
			t.Fatalf("unexpected literal result: %q %v (%d calls)", value, err, calls)
		}
	}
}

// Denied or unsupported formatting never turns a template into outbound display text
func TestFormatSettingTextFailsClosed(t *testing.T) {
	binding := Binding{Action: "example.show", Settings: map[string]any{"body": "${vault.alias}"}}
	denied := errors.New("denied")
	value, err := formatSettingText(t.Context(), func(context.Context, string, string) (string, error) {
		return "partial", denied
	}, binding, "body", "")
	if value != "" || !errors.Is(err, denied) {
		t.Fatalf("denied format returned %q %v", value, err)
	}
	if value, err := FormatSettingText(t.Context(), nil, binding, "body", ""); value != "" || err == nil {
		t.Fatalf("unsupported format returned %q %v", value, err)
	}
}
