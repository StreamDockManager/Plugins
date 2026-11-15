package capture

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestInteractiveCaptureAllowsSelectionAfterTenSeconds verifies interactive capture allows selection after ten seconds
func TestInteractiveCaptureAllowsSelectionAfterTenSeconds(t *testing.T) {
	t.Setenv("STREAM_DOCK_MANAGER_CAPTURE_COMMAND_FIXTURE", "slow")
	output, err := captureCommand(context.Background(), "region", os.Args[0], "-test.run=^TestCaptureCommandFixture$")
	if err != nil || output != "selected" {
		t.Fatal(output, err)
	}
}

// TestInteractiveCaptureHonorsCallerCancellation verifies interactive capture honors caller cancellation
func TestInteractiveCaptureHonorsCallerCancellation(t *testing.T) {
	t.Setenv("STREAM_DOCK_MANAGER_CAPTURE_COMMAND_FIXTURE", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	before := time.Now()
	_, err := captureCommand(ctx, "window", os.Args[0], "-test.run=^TestCaptureCommandFixture$")
	if err == nil || time.Since(before) > 5*time.Second {
		t.Fatal("capture ignored cancellation", err)
	}
}

// TestCaptureCommandFixture verifies capture command fixture
func TestCaptureCommandFixture(t *testing.T) {
	if os.Getenv("STREAM_DOCK_MANAGER_CAPTURE_COMMAND_FIXTURE") != "slow" {
		return
	}
	time.Sleep(11 * time.Second)
	fmt.Print("selected")
	os.Exit(0)
}
