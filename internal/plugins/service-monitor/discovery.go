package servicemonitor

import (
	"context"
	"runtime"
	"sort"
	"strings"
	"time"
)

type serviceChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Discover local service names using the platform adapter
func discoverServices(ctx context.Context) ([]serviceChoice, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var output string
	var err error
	switch runtime.GOOS {
	case "darwin":
		output, err = helper(ctx, "launchctl", "list")
	case "windows":
		output, err = helper(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Get-Service | Select-Object -ExpandProperty Name")
	default:
		output, err = helper(ctx, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager")
	}
	if err != nil {
		return nil, err
	}
	return serviceChoices(runtime.GOOS, output), nil
}

// Parse platform service output into inspector choices
func serviceChoices(platform, output string) []serviceChoice {
	names := make(map[string]struct{})
	for line := range strings.SplitSeq(output, "\n") {
		columns := strings.Fields(line)
		if len(columns) == 0 {
			continue
		}
		name := columns[0]
		switch platform {
		case "darwin":
			if len(columns) != 3 || columns[0] == "PID" {
				continue
			}
			name = columns[2]
		case "linux":
			if !strings.HasSuffix(name, ".service") {
				continue
			}
		}
		if serviceName.MatchString(name) {
			names[name] = struct{}{}
		}
	}
	ids := make([]string, 0, len(names))
	for name := range names {
		ids = append(ids, name)
	}
	sort.Strings(ids)
	limit := min(len(ids), 512)
	choices := make([]serviceChoice, 0, limit)
	for _, id := range ids[:limit] {
		choices = append(choices, serviceChoice{ID: id, Name: id, Kind: "service"})
	}
	return choices
}
