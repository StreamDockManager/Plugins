//go:build darwin

package vpn

import (
	"context"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// VPN reads the platform VPN connection status
func VPN(ctx context.Context) (string, error) { return process.Command(ctx, "scutil", "--nc", "list") }
