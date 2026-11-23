package randomizerdice

import (
	"context"
	"encoding/xml"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMagicBallLifecycleAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	ctx := context.Background()
	host := testkit.New(sdk.HostInfo{})
	e.State["shared"] = &saved{Value: 17, Color: "custom", Draws: []string{"used"}}
	send := func(kind string) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, "ball", "magic-8-ball", map[string]any{"shared_id": "shared"})); err != nil {
			t.Fatal(err)
		}
	}
	send("willAppear")
	send("keyDown")
	first := e.State["ball"].BallAnswer
	if first == "" || e.Last["ball"].Title != "Shaking…" {
		t.Fatal("missing shake")
	}
	clock.Advance(500 * time.Millisecond)
	send("keyDown")
	answer := e.State["ball"].BallAnswer
	if len(e.balls) != 1 {
		t.Fatal("reroll work unbounded")
	}
	clock.Advance(rollDuration)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	if e.Last["ball"].Title != answer || len(e.balls) != 0 || !e.NextPoll(clock.Now()).IsZero() {
		t.Fatal("wrong settled result")
	}
	shared := e.State["shared"]
	if shared.Value != 17 || shared.Color != "custom" || len(shared.Draws) != 1 {
		t.Fatal("shared dice/list/draw mutated")
	}
	if e.State["ball"].Value != 0 || e.State["ball"].Color != "" {
		t.Fatal("ball reused existing outcome fields")
	}
	send("keyDown")
	send("willDisappear")
	clock.Advance(rollDuration)
	if len(e.balls) != 0 {
		t.Fatal("hidden ball kept animation")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	restored := New(path)
	defer restored.Close()
	if restored.State["ball"].BallAnswer == "" || len(restored.balls) != 0 {
		t.Fatal("restart lost result or resumed shake")
	}
}
func TestMagicAnswersAreVariedAndBounded(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range ballAnswers {
		if a == "" || len(a) > 20 || seen[a] {
			t.Fatal("bad answer", a)
		}
		seen[a] = true
	}
	if len(seen) < 20 || !seen["Yes"] || !seen["Maybe"] || !seen["No"] {
		t.Fatal("missing variety")
	}
	if len(ActionFields("magic-8-ball")) != 0 {
		t.Fatal("ball exposes shared state")
	}
	if defaultListItems != "Yes\nNo" || strings.Count(defaultDeck, "\n") != 51 {
		t.Fatal("wrong List or Draw defaults")
	}
}

func TestMagicBallArtHidesAnswerUntilSettled(t *testing.T) {
	for _, answer := range ballAnswers {
		for frame := 0; frame <= rollFrameCount; frame++ {
			svg := ballGlyph(frame, answer)
			if strings.Contains(svg, "data-answer-panel") != (frame == rollFrameCount) {
				t.Fatal("wrong answer reveal phase", frame)
			}
			decoder := xml.NewDecoder(strings.NewReader("<svg>" + svg + "</svg>"))
			for {
				_, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(answer, frame, err)
				}
			}
		}
		lines := ballAnswerLines(answer)
		if strings.Join(lines, " ") != answer || len(lines) > 3 {
			t.Fatal("answer changed or too many lines", answer, lines)
		}
	}
}
