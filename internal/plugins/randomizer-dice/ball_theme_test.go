package randomizerdice

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBallPaletteFallbackSegmentsAndContrast(t *testing.T) {
	for _, frame := range []int{0, 15, 30} {
		original := ballGlyph(frame, "Yes")
		if tintBallSVG(original, sdk.ThemeColors{}) != original {
			t.Fatal("legacy colors changed")
		}
		if tintBallSVG(original, sdk.ThemeColors{Surface: "red", Ink: `"><script>`, Accent: "#123"}) != original {
			t.Fatal("invalid segment changed artwork")
		}
		themed := tintBallSVG(original, sdk.ThemeColors{Surface: "#eeeeee", Ink: "#eeeeee", Accent: "#eeeeee", Border: "#123456", Muted: "#654321"})
		if strings.Contains(themed, "#10131c") || !strings.Contains(themed, "#123456") {
			t.Fatal("palette not applied", frame)
		}
		if !strings.Contains(themed, "#000000") {
			t.Fatal("low contrast foreground not corrected", frame)
		}
	}
	partial := tintBallSVG(ballGlyph(30, "Yes"), sdk.ThemeColors{Border: "#123456"})
	if !strings.Contains(partial, `fill="#174fb1"`) || !strings.Contains(partial, `fill="#fff"`) {
		t.Fatal("missing segments lost exact fallback")
	}
	for _, background := range []string{"#000000", "#ffffff", "#808080", "#c61e32", "#abcdef"} {
		foreground := ballReadable(background, background)
		a, b := ballLuminance(foreground), ballLuminance(background)
		if (max(a, b)+.05)/(min(a, b)+.05) < 4.5 {
			t.Fatal("unreadable text", background, foreground)
		}
	}
}
func TestBallThemeEventRerendersIdleShakeAndAnswer(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	if err := e.Handle(ctx, host.Client, randomizerEvent("willAppear", "ball", "magic-8-ball", nil)); err != nil {
		t.Fatal(err)
	}
	for stage := 0; stage < 3; stage++ {
		if stage == 1 {
			if err := e.Handle(ctx, host.Client, randomizerEvent("keyDown", "ball", "magic-8-ball", nil)); err != nil {
				t.Fatal(err)
			}
		}
		if stage == 2 {
			clock.Advance(rollDuration)
			if err := e.Poll(ctx, host.Client); err != nil {
				t.Fatal(err)
			}
		}
		before := e.Last["ball"]
		host.Drain()
		// Exercise the live-theme authoring event with the already negotiated snapshot.
		e.themeColors = sdk.ThemeColors{Surface: []string{"#102030", "#203040", "#304050"}[stage], Accent: "#eeddcc", Ink: "#ffffff", Border: "#445566"}
		payload, _ := json.Marshal(map[string]any{"resolved": "dark"})
		if err := e.Handle(ctx, host.Client, sdk.Event{Event: sdk.ThemeEvent, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		after := e.Last["ball"]
		if before.Image == after.Image || before.Title != after.Title {
			t.Fatal("palette did not rerender same answer/phase", stage)
		}
		host.Drain()
		if err := e.Render(ctx, host.Client, "ball"); err != nil {
			t.Fatal(err)
		}
		if len(host.Drain()) != 0 {
			t.Fatal("identical palette/frame did not dedupe")
		}
	}
}
