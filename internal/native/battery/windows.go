//go:build windows

package battery

import (
	"context"
	"errors"
	"github.com/StreamDockManager/Plugins/internal/native/observation"
	"github.com/yusufpapurcu/wmi"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

var query = observation.Query{Slot: make(chan struct{}, 1)}

type batteryRow struct {
	Name                     *string
	EstimatedChargeRemaining *uint16
	BatteryStatus            *uint16
	EstimatedRunTime         *uint32
}

// Battery reads platform battery and power status
func Battery(ctx context.Context) (string, error) {
	text, err := query.Read(ctx, func() (string, error) {
		var rows []batteryRow
		client := wmi.Client{PtrNil: true}
		if err := client.Query("SELECT Name,EstimatedChargeRemaining,BatteryStatus,EstimatedRunTime FROM Win32_Battery", &rows); err != nil {
			return "", err
		}
		return observation.Rows(rows, "No battery")
	})
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return text, err
	}
	return ps(ctx, `$b=Get-CimInstance Win32_Battery; if(!$b){'No battery'} else {$b | Select-Object Name,EstimatedChargeRemaining,BatteryStatus,EstimatedRunTime | ConvertTo-Json -Compress}`)
}

// ps runs the platform helper through the bounded PowerShell adapter
func ps(ctx context.Context, s string) (string, error) {
	return process.Command(ctx, "powershell.exe", powershellArgs(s)...)
}
