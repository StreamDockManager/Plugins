// Package testhost supplies explicit metadata for the in-process mock host
package testhost

import (
	"context"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// Connect negotiates the extensions implemented by display/acceptance fixtures
// Theme updates are event-driven; fixtures do not advertise the theme RPC
func Connect(ctx context.Context, address, token string, handler sdk.Handler) error {
	info, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1","plugin-theme-v1","plugin-settings-v1","manager-controls-v1","action-results-v1","telemetry-v1","plugin-logs-v1","desktop-input-v1"]}}`))
	if err != nil {
		return err
	}
	return sdk.ConnectWithInfo(ctx, address, token, handler, info)
}
