package capture

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go/process"
	command "github.com/StreamDockManager/Plugins/internal/native/command"
)

// Region/window capture can wait for a user's selection. Keep an upper bound,
// but do not use the short deadline intended for unattended native queries
func captureCommand(ctx context.Context, mode, name string, args ...string) (string, error) {
	timeout := 10 * time.Second
	if mode == "region" || mode == "window" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := process.Capture(command.BackgroundCommandContext(ctx, name, args...), process.CaptureOptions{Truncate: true})
	if err != nil {
		return "", fmt.Errorf("capture failed or cancelled: %w (%s)", err, strings.TrimSpace(output))
	}
	return strings.TrimSpace(output), nil
}
