package engine

import (
	"context"
	"strings"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// FormatSettingText resolves one explicitly chosen text field without changing settings.
// Call only for display text or another documented transient destination, not identifiers,
// credentials, persistence, network routing, or executable arguments.
func FormatSettingText(ctx context.Context, client *sdk.Client, binding Binding, field, fallback string) (string, error) {
	if client == nil {
		return formatSettingText(ctx, nil, binding, field, fallback)
	}
	return formatSettingText(ctx, client.FormatText, binding, field, fallback)
}

// Keep the raw binding and all unselected fields outside of the host formatting request
func formatSettingText(ctx context.Context, format func(context.Context, string, string) (string, error), binding Binding, field, fallback string) (string, error) {
	text, ok := binding.Settings[field].(string)
	if !ok {
		text = fallback
	}
	if !strings.Contains(text, "${vault.") {
		return text, nil
	}
	if format == nil {
		return "", &sdk.UnsupportedFeatureError{Feature: sdk.TextReferencesProtocol}
	}
	resolved, err := format(ctx, binding.Action, text)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
