package command

import (
	"reflect"
	"testing"
)

// TestPowerShellKeepsScriptOneArgumentAndFreshPrefixes verifies power shell keeps script one argument and fresh prefixes
func TestPowerShellKeepsScriptOneArgumentAndFreshPrefixes(t *testing.T) {
	script := "Write-Output \"$env:USER; 'quoted'\"\nexit 0"
	expected := []string{"-NoProfile", "-NonInteractive", "-Command", script}
	args := PowerShellArgs(script)
	if !reflect.DeepEqual(args, expected) {
		t.Fatal(args)
	}
	args[0] = "changed"
	if !reflect.DeepEqual(PowerShellArgs(script), expected) {
		t.Fatal("argument slices alias")
	}
}
