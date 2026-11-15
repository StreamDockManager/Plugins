package command

import (
	"context"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/process"
	"strings"
	"time"
)

// Command dispatches the requested command through the adapter
func Command(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, e := process.Capture(BackgroundCommandContext(ctx, name, args...), process.CaptureOptions{Truncate: true})
	if e != nil {
		return "", fmt.Errorf("%s unavailable or denied: %w (%s)", name, e, strings.TrimSpace(b))
	}
	return strings.TrimSpace(b), nil
}
