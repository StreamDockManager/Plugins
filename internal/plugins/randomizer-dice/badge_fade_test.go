package randomizerdice

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
)

// Verify the same badge holds and fades on a numbered or large die without changing results
func TestResultBadgeFadesOnAllDice(t *testing.T) {
	for _, sides := range []int{2, 6, 20, 21, 9999, 1_000_000} {
		t.Run(fmt.Sprint(sides), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			clock := testkit.NewClock(time.Now())
			e.Now = clock.Now
			host := testkit.New(sdk.HostInfo{})
			ctx := context.Background()
			for _, counter := range []string{countCounterAction, totalCounterAction} {
				if err := e.Handle(ctx, host.Client, randomizerEvent("willAppear", counter, counter, nil)); err != nil {
					t.Fatal(err)
				}
			}
			ev := randomizerEvent("keyDown", "die", "dice", map[string]any{"sides": sides})
			if err := e.Handle(ctx, host.Client, ev); err != nil {
				t.Fatal(err)
			}
			step := func(delta time.Duration) {
				t.Helper()
				clock.Advance(delta)
				if err := e.Poll(ctx, host.Client); err != nil {
					t.Fatal(err)
				}
			}
			image := func() string {
				t.Helper()
				view := e.Last["die"]
				if icon := view.Feedback["icon"].(map[string]any)["value"]; icon != view.Image {
					t.Fatalf("image and live feedback differ")
				}
				raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(view.Image, "data:image/svg+xml;base64,"))
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}

			step(rollDuration)
			bare := image()
			if want := clock.Now().Add((badgeHoldFrames + 1) * rollFrameInterval); !e.NextPoll(clock.Now()).Equal(want) {
				t.Fatal("bare landing scheduled redundant frames during the badge hold")
			}
			step(badgeHoldFrames * rollFrameInterval)
			if e.rolls["die"].frame != rollFrameCount || image() != bare {
				t.Fatal("an unrelated poll redrew the held landing pose")
			}
			step(badgeScaleFrames * rollFrameInterval)
			full := image()
			result := int(e.State["die"].Value)
			if !strings.Contains(full, resultBadgeGlyph(result, 1, 1)) || strings.Count(full, `fill="#0f141d"`) != 1 || !strings.Contains(full, `r="9.2"`) {
				t.Fatal("missing shared full-opacity badge")
			}
			if want := clock.Now().Add(badgeVisibleFor + rollFrameInterval); !e.NextPoll(clock.Now()).Equal(want) {
				t.Fatal("hold did not park until the first fade frame")
			}
			step(badgeVisibleFor)
			if image() != full {
				t.Fatal("badge faded before its hold deadline")
			}
			step(5 * rollFrameInterval)
			if !strings.Contains(image(), resultBadgeGlyph(result, 1, .5)) {
				t.Fatal("badge opacity did not animate halfway")
			}
			step(5 * rollFrameInterval)
			withoutBadge := image()
			if strings.Contains(withoutBadge, "data-result-badge") || strings.Contains(withoutBadge, `fill="#0f141d"`) ||
				!strings.Contains(withoutBadge, dieResultGlyph(int64(sides), result, badgeFinalFrame, 0, false)) {
				t.Fatal("badge did not disappear while preserving the die")
			}
			if e.counterStatus().Text != "1" || e.totalCounterStatus().Text != strconv.Itoa(result) || !e.NextPoll(clock.Now()).IsZero() {
				t.Fatal("fading changed the counters or left background polling")
			}
			if view := e.Last["die"]; view.Feedback["line0"].(map[string]any)["value"] != "" || view.Title != fmt.Sprintf("d%d", sides) {
				t.Fatal("an alternate result line remained after the badge faded")
			}
		})
	}
}

// Verify the black badge uses light digits regardless of the selected theme
func TestBadgeInkRemainsReadableInLightPalette(t *testing.T) {
	e := &Plugin{themeColors: sdk.ThemeColors{Ink: "#09090b"}}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(e.dieSVG(resultBadgeGlyph(9999, 1, 1)), "data:image/svg+xml;base64,"))
	if err != nil || !strings.Contains(string(raw), `fill="#0f141d"`) ||
		!strings.Contains(string(raw), `fill="#f7f8fa"`) || !strings.Contains(string(raw), `data-outline-text="9999"`) {
		t.Fatal("the four-digit result badge lost its high-contrast artwork", err)
	}
}

// Verify baked tumble and idle frames cannot bring back a second result panel
func TestPackagedDiceHaveNoLegacyResultPanels(t *testing.T) {
	for path, glyph := range RollGlyphs() {
		if strings.Contains(glyph, `fill="#0f141d"`) || strings.Contains(glyph, "data-result-badge") {
			t.Fatalf("%s contains a baked result panel", path)
		}
	}
}
