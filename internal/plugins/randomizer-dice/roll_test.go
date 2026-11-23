package randomizerdice

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
)

func TestDieAnimationRevealsSavedRoll(t *testing.T) {
	for _, sides := range []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 17, 20, 21, 24, 30, 37, 48, 60, 100, 120, 9999, 1_000_000} {
		t.Run(fmt.Sprintf("d%d", sides), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			e := New(path)
			clock := testkit.NewClock(time.Now())
			e.Now = clock.Now
			host := testkit.New(sdk.HostInfo{})
			ctx := context.Background()
			settings := map[string]any{"sides": sides}
			if sides == 6 {
				settings = nil // Exercise the published default.
			}
			ev := randomizerEvent("willAppear", "key", "dice", settings)
			if err := e.Handle(ctx, host.Client, ev); err != nil {
				t.Fatal(err)
			}
			ev.Event = "keyDown"
			if err := e.Handle(ctx, host.Client, ev); err != nil {
				t.Fatal(err)
			}
			result := int(e.State["key"].Value)
			if result < 1 || result > sides {
				t.Fatalf("rolled %d on d%d", result, sides)
			}
			if e.Last["key"].Title != fmt.Sprintf("Rolling d%d", sides) {
				t.Fatal("die did not start rolling", e.Last["key"].Title)
			}
			start := clock.Now()
			var lastImage time.Time
			images := map[string]bool{}
			for {
				messages := host.Drain()
				if image := visualPayload(messages, "key", "setImage"); image != nil {
					if !lastImage.IsZero() && clock.Now().Sub(lastImage) > 50*time.Millisecond && clock.Now().Sub(start) < rollDuration {
						t.Fatal("visible gap in the tumble", clock.Now().Sub(lastImage))
					}
					lastImage = clock.Now()
					path := image["image"].(string)
					icon := visualPayload(messages, "key", "setFeedback")["icon"].(map[string]any)
					if path != icon["value"] {
						t.Fatal("image and feedback poses disagree", path, icon)
					}
					images[path] = true
				}
				if len(e.rolls) == 0 {
					break
				}
				next := e.NextPoll(clock.Now())
				if next.IsZero() {
					break
				}
				if !next.After(clock.Now()) || next.Sub(start) > 1800*time.Millisecond {
					t.Fatal("animation failed to settle", next.Sub(start))
				}
				clock.Advance(next.Sub(clock.Now()))
				if err := e.Poll(ctx, host.Client); err != nil {
					t.Fatal(err)
				}
			}
			view := e.Last["key"]
			if len(images) < 24 || lastImage.Sub(start) < time.Second || view.Title != fmt.Sprintf("d%d", sides) ||
				view.Feedback["line0"].(map[string]any)["value"] != "" ||
				view.Feedback["line1"].(map[string]any)["value"] != fmt.Sprintf("d%d", sides) {
				t.Fatalf("saw %d die poses, final feedback %v for roll %d", len(images), view, result)
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(view.Image, "data:image/svg+xml;base64,"))
			if err != nil || !strings.Contains(string(raw), dieResultGlyph(int64(sides), result, badgeFinalFrame, 0, true)) {
				t.Fatal("the result badge or settled artwork does not match the persisted result", result, err)
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			restored := New(path)
			defer restored.Close()
			if restored.State["key"].Value != float64(result) || !restored.NextPoll(clock.Now()).IsZero() {
				t.Fatal("restart did not retain just the settled result")
			}
		})
	}
}

func TestHighDiceUseDistinctSolids(t *testing.T) {
	glyphs := RollGlyphs()
	resting := map[string]bool{}
	for _, sides := range highDieShapes {
		mesh := highDieMesh(sides)
		if int64(len(mesh)) != sides {
			t.Fatalf("d%d has %d faces", sides, len(mesh))
		}
		glyph := glyphs[dieIcon(sides, rollFrameCount)]
		if glyph == "" || resting[glyph] {
			t.Fatalf("d%d has missing or repeated artwork", sides)
		}
		resting[glyph] = true
	}
	for _, sides := range []int64{21, 37, 99, 9999} {
		glyph := glyphs[dieIcon(sides, rollFrameCount)]
		if !resting[glyph] {
			t.Fatalf("custom d%d did not select high-sided artwork", sides)
		}
	}
	if large := glyphs[dieIcon(1_000_000, rollFrameCount)]; large == "" || resting[large] {
		t.Fatal("large custom die is missing its separate result layout artwork")
	}
}

func TestSmallDiceHaveNumberedFacets(t *testing.T) {
	glyphs := RollGlyphs()
	seen := map[string]bool{}
	for sides := int64(2); sides <= 20; sides++ {
		m := modelFor(sides)
		numbers := make(map[int]bool)
		for i, f := range m.faces {
			value := m.marks[i]
			if value != 0 {
				if value < 1 || value > int(sides) || numbers[value] {
					t.Fatalf("d%d face %d has invalid or duplicate mark %d", sides, i, value)
				}
				numbers[value] = true
			}
			n := f[1].sub(f[0]).cross(f[2].sub(f[0])).unit()
			for _, p := range f {
				if distance := n.dot(p.sub(f[0])); math.IsNaN(distance) || math.Abs(distance) > 1e-8 {
					t.Fatalf("d%d facet %d is not planar", sides, i)
				}
			}
		}
		if len(numbers) != int(sides) {
			t.Fatalf("d%d has %d numbered faces", sides, len(numbers))
		}
		glyph := glyphs[dieIcon(sides, 0)]
		if seen[glyph] || !strings.Contains(glyph, `data-sdm-fill="accent"`) || !strings.Contains(glyph, `data-sdm-stroke="ink"`) {
			t.Fatalf("d%d has repeated or unshaded artwork", sides)
		}
		seen[glyph] = true
	}
	for i, marks := range cubeMarks {
		if marks[1] != i+1 || marks[0]+marks[1] != 7 || marks[2]+marks[3] != 7 || marks[4]+marks[5] != 7 {
			t.Fatalf("invalid d6 orientation %v for result %d", marks, i+1)
		}
		end := glyphs[dieFaceIcon(i+1)]
		if end == "" || seen[end] || end != glyphs[cubeFrameIcon(i+1, rollFrameCount-1)] {
			t.Fatalf("d6 roll %d does not land on its matching pip face", i+1)
		}
		seen[end] = true
	}
}

func TestRollDeadlinesBelongToLatestPressOnEachBinding(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind, id, name string, settings map[string]any) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, id, name, settings)); err != nil {
			t.Fatal(err)
		}
		host.Drain()
	}
	shared := map[string]any{"shared_id": "table", "sides": 20}
	send("keyDown", "a", "dice", shared)
	send("dialDown", "b", "dice", shared)
	resultB := int(e.State["table"].Value)
	duration := time.Duration(badgeFinalFrame) * rollFrameInterval
	clock.Advance(duration / 2)
	send("touchTap", "a", "dice", shared)
	resultA := int(e.State["table"].Value)
	send("keyDown", "range", "range", map[string]any{"shared_id": "table", "min": -3, "max": -3})
	clock.Advance(duration / 2)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	updates := host.Drain()
	requireUpdatedContexts(t, updates, "a", "b")
	if len(e.rolls) != 1 || e.Last["b"].Title != "d20" || e.Results["b"] != float64(resultB) {
		t.Fatal("a reroll or shared state replaced another press's display", e.Last)
	}
	clock.Advance(time.Duration(badgeFinalFrame) * rollFrameInterval) // Drop all missed poses and reveal the newest roll.
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	requireUpdatedContexts(t, host.Drain(), "a")
	if e.Last["a"].Title != "d20" || e.Results["a"] != float64(resultA) || e.State["table"].Value != -3 {
		t.Fatal("animation read or rewrote the shared range result")
	}

	send("keyDown", "a", "dice", nil)
	send("keyDown", "b", "dice", nil)
	send("keyDown", "c", "dice", shared)
	send("willDisappear", "a", "dice", nil)
	send("didReceiveSettings", "b", "dice", map[string]any{"sides": 4})
	send("willAppear", "c", "dice", map[string]any{"sides": 8})
	clock.Advance(time.Duration(badgeFinalFrame) * rollFrameInterval)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	if len(host.Drain()) != 0 || !e.NextPoll(clock.Now()).IsZero() || e.Last["c"].Title != "d8" {
		t.Fatal("retired or reconfigured bindings retained animation work")
	}
}

func TestRollMotionVariesWithoutChangingTheSampledResult(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	host := testkit.New(sdk.HostInfo{})
	seen := map[int]bool{}
	for i := 0; i < 8; i++ {
		if err := e.Handle(context.Background(), host.Client, randomizerEvent("keyDown", "key", "dice", map[string]any{"sides": 2})); err != nil {
			t.Fatal(err)
		}
		roll := e.rolls["key"]
		seen[roll.variant] = true
		if float64(roll.result) != e.State["key"].Value || e.Results["key"] != e.State["key"].Value {
			t.Fatal("motion changed sampled/persisted result")
		}
		if len(e.rolls) != 1 {
			t.Fatal("replaced roll retained pending work")
		}
	}
	if len(seen) != landingVariants {
		t.Fatal("repeated rolls did not vary motion", seen)
	}
}

func TestInlineDieThemeUsesValidatedNegotiatedColors(t *testing.T) {
	svg := landingGlyph(6, 6, rollFrameCount, 0)
	themed := tintLandingSVG(svg, sdk.ThemeColors{Accent: "#123456", Ink: "#abcdef"})
	if !strings.Contains(themed, `fill="#123456"`) || !strings.Contains(themed, `stroke="#abcdef"`) {
		t.Fatal("inline die lost palette colors")
	}
	if tintLandingSVG(svg, sdk.ThemeColors{Accent: `"/><script/>`, Ink: "invalid"}) != svg {
		t.Fatal("invalid theme injected markup")
	}
}
