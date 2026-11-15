//go:build darwin

package battery

import (
	"context"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// Battery reads platform battery and power status
func Battery(ctx context.Context) (string, error) { return process.Command(ctx, "pmset", "-g", "batt") }
