package engine

import "testing"

// TestObservedMuteStateTakesPriorityOverDeviceAndDriverNames verifies observed mute state takes priority over device and driver names
func TestObservedMuteStateTakesPriorityOverDeviceAndDriverNames(t *testing.T) {
	for _, action := range []string{"mute", "unmute", "toggle"} {
		p := presentation("microphone-controls", action, DisplayStatus{Text: "Long named desk microphone\nMuted\nSystem input"})
		if p.Primary != "Muted" || p.Detail != "" {
			t.Fatal(action, p)
		}
	}
}

// TestExactValuesSurviveAndCommandDisplaysOmitVerboseResults verifies exact values survive and command displays omit verbose results
func TestExactValuesSurviveAndCommandDisplaysOmitVerboseResults(t *testing.T) {
	p := presentation("counter-tally", "increment", DisplayStatus{Text: "123456789012345"})
	if p.Primary != "123456789012345" {
		t.Fatal(p)
	}
	p = presentation("http-webhooks", "request", DisplayStatus{Text: "HTTP 200\n{\"private\":\"response body\"}"})
	if p.Primary != "" || p.Detail != "" || p.Kind != "command" {
		t.Fatal(p)
	}
}
