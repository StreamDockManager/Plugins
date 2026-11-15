//go:build linux

package battery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Battery reads platform battery and power status
func Battery(ctx context.Context) (string, error) {
	paths, _ := filepath.Glob("/sys/class/power_supply/*/type")
	out := []string{}
	for _, p := range paths {
		kind, _ := os.ReadFile(p)
		if strings.TrimSpace(string(kind)) != "Battery" {
			continue
		}
		root := filepath.Dir(p)
		charge, _ := os.ReadFile(filepath.Join(root, "capacity"))
		state, _ := os.ReadFile(filepath.Join(root, "status"))
		estimate := ""
		energy, _ := os.ReadFile(filepath.Join(root, "energy_now"))
		power, _ := os.ReadFile(filepath.Join(root, "power_now"))
		if e, x := strconv.ParseFloat(strings.TrimSpace(string(energy)), 64); x == nil {
			if p, x := strconv.ParseFloat(strings.TrimSpace(string(power)), 64); x == nil && p > 0 && strings.TrimSpace(string(state)) == "Discharging" {
				estimate = fmt.Sprintf(" (%.1fh remaining estimate)", e/p)
			}
		}
		out = append(out, fmt.Sprintf("%s %s%% %s%s", filepath.Base(root), strings.TrimSpace(string(charge)), strings.TrimSpace(string(state)), estimate))
	}
	if len(out) == 0 {
		return "No battery", nil
	}
	return strings.Join(out, "\n"), nil
}
