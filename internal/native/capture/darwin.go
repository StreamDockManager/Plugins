//go:build darwin

package capture

import (
	"context"
	"fmt"
)

// Capture captures the selected display through the platform backend
func Capture(ctx context.Context, mode, path string) error {
	args := []string{"-x"}
	switch mode {
	case "region":
		args = append(args, "-i")
	case "window":
		args = append(args, "-i", "-w")
	case "clipboard":
		args = append(args, "-c")
	case "display":
	default:
		return fmt.Errorf("unknown capture mode")
	}
	if mode != "clipboard" {
		args = append(args, path)
	}
	_, e := captureCommand(ctx, mode, "screencapture", args...)
	return e
}
