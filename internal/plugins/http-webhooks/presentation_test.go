package httpwebhooks

import (
	"path/filepath"
	"testing"
)

// Verify HTTP observation produces structured presentation
func TestHTTPObservationProducesStructuredPresentation(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "state.json"))
	defer p.Close()
	p.Results["key"] = HTTPResult{Status: 204, DurationMS: 17}
	// The structured fields, not incidental diagnostic text, determine the rows
	p.ResultsText["key"] = "diagnostic with unrelated words"
	for _, tc := range []struct{ action, primary, label, text string }{{"status", "204", "Status", "HTTP 204"}, {"duration", "17 ms", "Duration", "17 ms"}} {
		s := p.extensionStatus("key", binding{Action: tc.action})
		if s.Content == nil || s.Content.Primary != tc.primary || s.Content.Label != tc.label || s.Text != tc.text {
			t.Fatal(s)
		}
	}
	p.ResultsText["key"] = "Stale\nlast observation"
	p.polling["key"] = pollingState{Stale: true, Failures: 1}
	if s := p.extensionStatus("key", binding{Action: "status"}); s.Content != nil || s.Text != "Stale\nlast observation" {
		t.Fatal("stale fallback changed", s)
	}
}
