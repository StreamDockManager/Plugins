package randomizerdice

import (
	"context"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Check permanent face markings and exact final pose continuity
func TestCoinFacesRemainAttachedAndLand(t *testing.T) {
	for face := 1; face <= 2; face++ {
		for variant := 0; variant < 4; variant++ {
			model := landingModels[2]
			rotate := landingRotation(model, face, rollFrameCount, variant)
			for i, value := range model.marks {
				if value > 0 && rotate(outwardNormal(model.faces[i])).z > .9 && value != face {
					t.Fatal("wrong landed face")
				}
			}
			if coinGlyph(face, 29, variant) != coinGlyph(face, 30, variant) {
				t.Fatal("landing swap")
			}
			glyph := coinGlyph(face, 30, variant)
			if strings.Count(glyph, `stroke-opacity=".42"`) != 1 {
				t.Fatal("a face-on coin must not render degenerate rim faces")
			}
			mark := "H"
			if face == 2 {
				mark = "T"
			}
			if !strings.Contains(glyph, `data-outline-text="`+mark+`"`) {
				t.Fatal("missing engraved mark")
			}
		}
	}
}

// Check rerolls, persistence and isolation from dice aggregates
func TestCoinLifecycleIsIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind string) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, "coin", "heads-or-tails", map[string]any{"shared_id": "other"})); err != nil {
			t.Fatal(err)
		}
	}
	e.State["other"] = &saved{Value: 17}
	e.State[counterStateKey] = &saved{RollCount: 8, RollTotal: 42}
	send("willAppear")
	send("keyDown")
	if e.coinStatus("coin").Text != "Flipping…" {
		t.Fatal("no immediate animation")
	}
	send("keyDown")
	if len(e.coins) != 1 || len(e.rolls) != 0 {
		t.Fatal("reroll isolation")
	}
	if e.Last["coin"].Title != "" {
		t.Fatal("keypad title would duplicate image label")
	}
	face := e.State["coin"].CoinFace
	clock.Advance(rollDuration)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	if e.coinStatus("coin").Text != coinLabel(face) || !e.NextPoll(clock.Now()).IsZero() {
		t.Fatal("reveal/deadline")
	}
	if e.State["other"].Value != 17 || e.State[counterStateKey].RollCount != 8 || e.State[counterStateKey].RollTotal != 42 {
		t.Fatal("unrelated aggregate changed")
	}
	send("keyDown")
	send("willDisappear")
	if len(e.coins) != 0 {
		t.Fatal("hidden animation retained")
	}
	face = e.State["coin"].CoinFace
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	restored := New(path)
	defer restored.Close()
	if restored.State["coin"].CoinFace != face {
		t.Fatal("not persisted")
	}
}
