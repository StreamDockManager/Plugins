//go:build windows

package command

import "syscall"

// testConsoleWindow reports the platform console-window state for the test
func testConsoleWindow() uintptr {
	window, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	return window
}
