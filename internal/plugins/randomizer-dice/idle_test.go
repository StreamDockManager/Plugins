package randomizerdice

import (
	"context"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdleDiceEmitsConfiguredShapeBeforeFirstRoll(t *testing.T) {
	for _, sides := range []int{2, 6, 10, 17, 20, 100} {
		t.Run(fmt.Sprint(sides), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			host := testkit.New(sdk.HostInfo{})
			ctx := context.Background()
			nextSides := sides + 1
			if sides > 20 {
				nextSides = 10
			}
			for _, step := range []struct {
				event string
				sides int
			}{{"willAppear", sides}, {"didReceiveSettings", nextSides}} {
				if err := e.Handle(ctx, host.Client, randomizerEvent(step.event, "die", "dice", map[string]any{"sides": step.sides})); err != nil {
					t.Fatal(err)
				}
				messages := host.Drain()
				image := visualPayload(messages, "die", "setImage")
				feedback := visualPayload(messages, "die", "setFeedback")
				if image == nil || feedback == nil {
					t.Fatal("idle rendering suppressed", step)
				}
				want := dieIcon(int64(step.sides), rollLandingFrame)
				if !strings.Contains(image["image"].(string), want) {
					t.Fatalf("wrong idle model: %v want %s", image, want)
				}
				if feedback["icon"].(map[string]any)["value"] != image["image"] {
					t.Fatal("feedback/image diverged")
				}
				if e.Last["die"].Title != fmt.Sprintf("d%d", step.sides) {
					t.Fatal("wrong idle label")
				}
				if len(e.rolls) != 0 || e.lookupState("die", nil).Value != 0 || e.lookupState(counterStateKey, nil).RollCount != 0 {
					t.Fatal("appearance fabricated a roll")
				}
			}
		})
	}
}

func TestIdleDiceRetainsValidStoredResultAndRejectsOutOfRangeDisplay(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.state("die", nil).Value = 7
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	if err := e.Handle(ctx, host.Client, randomizerEvent("willAppear", "die", "dice", map[string]any{"sides": 20})); err != nil {
		t.Fatal(err)
	}
	if e.Last["die"].Title != "d20" || e.Last["die"].Image != e.dieSVG(landingGlyph(20, 7, badgeFinalFrame, 0)) {
		t.Fatal("stored result not restored")
	}
	if err := e.Handle(ctx, host.Client, randomizerEvent("didReceiveSettings", "die", "dice", map[string]any{"sides": 2})); err != nil {
		t.Fatal(err)
	}
	if e.Last["die"].Title != "d2" || e.lookupState("die", nil).Value != 7 {
		t.Fatal("reconfiguration overwrote stored result or showed invalid face")
	}
}
