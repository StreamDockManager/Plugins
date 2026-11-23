//go:build darwin

package battery

import (
	"context"
	"github.com/StreamDockManager/Plugins/internal/native/observation"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

var monitor observation.Monitor

// Battery reads platform battery and power status
func Battery(ctx context.Context) (string, error) {
	return monitor.Read(ctx, observation.PowerChanges, func(ctx context.Context) (string, error) { return process.Command(ctx, "pmset", "-g", "batt") })
}
