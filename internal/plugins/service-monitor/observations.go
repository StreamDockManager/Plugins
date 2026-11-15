package servicemonitor

import (
	"fmt"
	"regexp"
	"strings"
)

var windowsServiceState = regexp.MustCompile(`(?m)STATE\s*:\s*([1-7])\s+([A-Z_]+)`)

// Interpret a service-status observation without assuming command success
func localServiceObservation(platform, name, text string, commandErr error) (bool, string, error) {
	switch platform {
	case "linux":
		// systemctl uses a nonzero exit code for known inactive states
		switch strings.TrimSpace(text) {
		case "active":
			return true, "", nil
		case "inactive", "failed", "activating", "deactivating", "reloading":
			return false, strings.TrimSpace(text), nil
		}
	case "windows":
		if commandErr == nil {
			if state := windowsServiceState.FindStringSubmatch(text); len(state) == 3 {
				return state[1] == "4", state[2], nil
			}
		}
	case "darwin":
		if commandErr == nil {
			for line := range strings.SplitSeq(text, "\n") {
				columns := strings.Fields(line)
				if len(columns) >= 3 && columns[2] == name {
					if columns[0] == "-" {
						return false, "stopped", nil
					}
					return true, "", nil
				}
			}
			if strings.Contains(text, "PID") && strings.Contains(text, "Label") {
				return false, "not loaded", nil
			}
		}
	}
	if commandErr != nil {
		return false, "", commandErr
	}
	return false, "", fmt.Errorf("unrecognized local service status")
}
