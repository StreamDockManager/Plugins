package vlc

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// Verify invalid rate does not discard reported position
func TestInvalidRateDoesNotDiscardReportedPosition(t *testing.T) {
	for _, rate := range []string{`"fast"`, `false`, `null`, `{}`, `-1`, `1e999`} {
		var status Status
		if err := json.Unmarshal([]byte(`{"state":"playing","time":42,"length":100,"volume":128,"rate":`+rate+`}`), &status); err != nil {
			t.Fatal(err)
		}
		if status.Rate != nil || status.Time == nil || *status.Time != 42 {
			t.Fatal(rate, status)
		}
	}
}

// Verify progress rate samples pause and missing rate
func TestProgressRateSamplesPauseAndMissingRate(t *testing.T) {
	observed := time.Unix(1000, 0)
	d := &Driver{Now: func() time.Time { return observed.Add(10 * time.Second) }}
	position, length := 20.0, 100.0
	for _, tc := range []struct {
		rate        float64
		state, want string
	}{{.5, "playing", "25s"}, {2, "playing", "40s"}, {20, "playing", "100s"}, {2, "paused", "20s"}, {0, "playing", "20s"}, {-1, "playing", "20s"}, {math.NaN(), "playing", "20s"}, {math.Inf(1), "playing", "20s"}} {
		v := Status{Time: &position, Length: &length, State: tc.state, Observed: observed, Rate: &tc.rate}
		if text := d.Display(v, "progress", nil); !strings.Contains(text, "\n"+tc.want+" / 100s") {
			t.Fatal(tc, text)
		}
	}
	if text := d.Display(Status{Time: &position, Length: &length, State: "playing", Observed: observed}, "progress", nil); !strings.Contains(text, "20s / 100s") {
		t.Fatal(text)
	}
}
