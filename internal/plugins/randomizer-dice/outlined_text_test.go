package randomizerdice

import (
	"fmt"
	"strings"
	"testing"
)

func TestAnswerAndBadgeOutlinesNeedNoHostFonts(t *testing.T) {
	for _, answer := range ballAnswers {
		for _, r := range answer {
			glyph, ok := answerGlyphs[r]
			if !ok || glyph.width <= 0 || r != ' ' && glyph.path == "" {
				t.Fatalf("missing glyph %q", r)
			}
		}
		svg := ballGlyph(rollFrameCount, answer)
		if strings.Contains(svg, "<text") || !strings.Contains(svg, "data-outline-text") {
			t.Fatal("font-dependent answer", answer)
		}
	}
	for result := 1; result <= 20; result++ {
		svg := landingDisplayGlyph(20, result, badgeFinalFrame, 0)
		if strings.Contains(svg, "<text") || !strings.Contains(svg, fmt.Sprintf(`data-outline-text="%d"`, result)) {
			t.Fatal("font-dependent badge", result)
		}
	}
}
