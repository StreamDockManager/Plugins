//go:build windows

package vpn

import (
	"context"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// VPN reads the platform VPN connection status
func VPN(ctx context.Context) (string, error) {
	return ps(ctx, `Get-VpnConnection | Select-Object Name,ConnectionStatus | ConvertTo-Json -Compress`)
}

// ps runs the platform helper through the bounded PowerShell adapter
func ps(ctx context.Context, s string) (string, error) {
	return process.Command(ctx, "powershell.exe", powershellArgs(s)...)
}
