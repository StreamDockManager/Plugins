//go:build linux

package vpn

import (
	"context"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// VPN reads the platform VPN connection status
func VPN(ctx context.Context) (string, error) {
	return process.Command(ctx, "nmcli", "-t", "-f", "NAME,TYPE,STATE", "connection", "show", "--active")
}
