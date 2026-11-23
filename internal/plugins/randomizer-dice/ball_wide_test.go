package randomizerdice

import (
	"encoding/base64"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"path/filepath"
	"strings"
	"testing"
)

func TestBallWideArtworkAndFeedback(t *testing.T) {
	for _, size := range [][2]int{{176, 112}, {200, 100}, {352, 112}, {72, 72}} {
		e := New(filepath.Join(t.TempDir(), "state.json"))
		e.Bindings["ball"] = binding{Action: "magic-8-ball", Surface: &sdk.Surface{Width: size[0], Height: size[1]}}
		for _, answer := range append([]string{""}, ballAnswers...) {
			e.state("ball", nil).BallAnswer = answer
			status := e.ballStatus("ball")
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(status.Icon, "data:image/svg+xml;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			svg := string(raw)
			wide := size[0] > size[1]*13/10
			if strings.Contains(svg, `data-wide-ball="true"`) != wide {
				t.Fatal("wrong layout", size)
			}
			if wide {
				if !strings.Contains(svg, fmt.Sprintf(`viewBox="0 0 %.4f 100"`, 100*float64(size[0])/float64(size[1]))) || strings.Contains(svg, "<text") {
					t.Fatal("aspect or font dependency", size)
				}
				view := e.displayView("ball", status)
				if view.Feedback["icon"].(map[string]any)["rect"] != [4]int{0, 0, 200, 100} || view.Feedback["line0"].(map[string]any)["value"] != "" || view.Feedback["line1"].(map[string]any)["value"] != "" {
					t.Fatal("duplicate host caption or wrong geometry")
				}
				if strings.Join(ballWideLines(answer), " ") != answer {
					t.Fatal("answer changed")
				}
			}
		}
		e.Close()
	}
}

func TestBallWideShakeDoesNotRevealAnswer(t *testing.T) {
	a, b := ballWideSVG(176, 112, 3, "Outlook poor"), ballWideSVG(176, 112, 11, "Outlook poor")
	if a == b || !strings.Contains(a, `data-outline-text="Shaking"`) || strings.Contains(a, "Outlook") {
		t.Fatal("shake missing or early answer")
	}
	for _, text := range []string{"Ask a question", "Shaking"} {
		for _, r := range text {
			if _, ok := answerGlyphs[r]; !ok {
				t.Fatalf("missing outlined glyph %q", r)
			}
		}
	}
	themed := tintBallSVG(ballWideSVG(176, 112, rollFrameCount, "Outlook poor"), sdk.ThemeColors{Accent: "#eeeeee", Ink: "#eeeeee"})
	if !strings.Contains(themed, `fill="#000000"`) {
		t.Fatal("answer contrast not retained")
	}
}
