//go:build darwin

package vpn

import (
	"context"
	"github.com/StreamDockManager/Plugins/internal/native/observation"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

var monitor observation.Monitor

// VPN reads the platform VPN connection status
func VPN(ctx context.Context) (string, error) {
	return monitor.Read(ctx, observation.NetworkChanges, func(ctx context.Context) (string, error) { return process.Command(ctx, "scutil", "--nc", "list") })
}
