//go:build !windows

package command

// testConsoleWindow reports the platform console-window state for the test
func testConsoleWindow() uintptr { return 0 }
