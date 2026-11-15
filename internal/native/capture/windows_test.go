//go:build windows

package capture

import (
	"context"
	"testing"
)

// TestPowerShellCaptureFailsOnNonTerminatingErrors verifies power shell capture fails on non terminating errors
func TestPowerShellCaptureFailsOnNonTerminatingErrors(t *testing.T) {
	_, err := ps(context.Background(), "clipboard", `Write-Error 'fixture failure'; Write-Output 'unreachable'`)
	if err == nil {
		t.Fatal("capture helper ignored a PowerShell error")
	}
}

// TestPowerShellCaptureUsesSingleThreadedApartment verifies power shell capture uses single threaded apartment
func TestPowerShellCaptureUsesSingleThreadedApartment(t *testing.T) {
	output, err := ps(context.Background(), "clipboard", `[Threading.Thread]::CurrentThread.ApartmentState`)
	if err != nil || output != "STA" {
		t.Fatal("clipboard helper requires STA", output, err)
	}
}
