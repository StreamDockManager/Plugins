//go:build linux

package capture

import (
	"context"
	"fmt"
)

// Capture captures the selected display through the platform backend
func Capture(context.Context, string, string) error {
	return fmt.Errorf("Screenshot Tools supports Windows 11 and macOS 14 or later; Linux is unsupported")
}
