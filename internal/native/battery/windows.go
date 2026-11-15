//go:build windows

package battery

import (
	"context"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// Battery reads platform battery and power status
func Battery(ctx context.Context) (string, error) {
	return ps(ctx, `$b=Get-CimInstance Win32_Battery; if(!$b){'No battery'} else {$b | Select-Object Name,EstimatedChargeRemaining,BatteryStatus,EstimatedRunTime | ConvertTo-Json -Compress}`)
}

// ps runs the platform helper through the bounded PowerShell adapter
func ps(ctx context.Context, s string) (string, error) {
	return process.Command(ctx, "powershell.exe", powershellArgs(s)...)
}
