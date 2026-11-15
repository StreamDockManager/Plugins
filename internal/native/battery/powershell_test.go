package battery

import (
	"reflect"
	"testing"
)

// TestPowerShellArguments verifies power shell arguments
func TestPowerShellArguments(t *testing.T) {
	script := "Write-Output 'fixture'"
	want := []string{"-NoProfile", "-NonInteractive", "-Command", script}
	if got := powershellArgs(script); !reflect.DeepEqual(got, want) {
		t.Fatalf("PowerShell args = %q, want %q", got, want)
	}
}
