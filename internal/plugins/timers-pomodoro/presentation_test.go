package timerspomodoro

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// Verify structured timer presentation
func TestStructuredTimerPresentation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		state                saved
		text, primary, label string
	}{
		{"ready", saved{}, "Ready\n00:00", "00:00", "Ready"},
		{"running", saved{Mode: "countdown", Deadline: 161000}, "countdown\n01:01", "01:01", "countdown"},
		{"break", saved{Mode: "pomodoro", Break: true, Deadline: 160000}, "Break\n01:00", "01:00", "Break"},
		{"paused", saved{Mode: "stopwatch", Elapsed: 125000}, "stopwatch\n02:05", "02:05", "stopwatch"},
		{"expired", saved{Mode: "countdown", Deadline: 1}, "countdown\n00:00", "00:00", "countdown"},
		{"done", saved{Color: "Done"}, "Done", "Done", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, p := timerContent(&tc.state, 100000)
			if text != tc.text || p.Primary != tc.primary || p.Label != tc.label || p.Kind != "progress" {
				t.Fatal(text, p)
			}
		})
	}
}

// Verify timer structured content matches legacy rows
func TestTimerStructuredContentMatchesLegacyRows(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	for _, action := range []string{"countdown", "pomodoro", "stopwatch"} {
		for _, state := range []saved{{Mode: action, Elapsed: 123000}, {Mode: action, Break: true, Elapsed: 45000}, {Color: "Done"}} {
			p.State["key"] = &state
			p.Bindings["key"] = binding{Action: action, Settings: map[string]any{}}
			status := p.displayStatus(context.Background(), "key", p.Bindings["key"])
			structured := p.DisplayView("key", status)
			status.Content = nil
			legacy := p.DisplayView("key", status)
			if !reflect.DeepEqual(structured, legacy) {
				t.Fatalf("%s %+v changed display rows\n%+v\n%+v", action, state, structured, legacy)
			}
		}
	}
}
