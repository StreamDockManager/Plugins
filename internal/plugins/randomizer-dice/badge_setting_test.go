package randomizerdice

import (
	"context"
	"encoding/base64"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBadgePreferenceDefaultsAndScope(t *testing.T) {
	for _, value := range []any{nil, "false", 0, 1, []any{}, true} {
		if !showResultBadge(map[string]any{"show_result_badge": value}) {
			t.Fatal("invalid/missing should default on", value)
		}
	}
	if showResultBadge(map[string]any{"show_result_badge": false}) {
		t.Fatal("false ignored")
	}
	fields := ActionFields("dice")
	if len(fields) != 3 || fields[2].Key != "show_result_badge" || fields[2].Type != "checkbox" || fields[2].Default != true {
		t.Fatal("missing default-on inspector schema", fields)
	}
}

func TestBadgeOffEndsAtTumbleAndReconfiguresSafely(t *testing.T) {
	for _, sides := range []int{2, 6, 20, 21, 100} {
		t.Run(fmt.Sprint(sides), func(t *testing.T) {
			e := New(filepath.Join(t.TempDir(), "state.json"))
			defer e.Close()
			clock := testkit.NewClock(time.Now())
			e.Now = clock.Now
			host := testkit.New(sdk.HostInfo{})
			ctx := context.Background()
			settings := map[string]any{"sides": sides, "show_result_badge": false}
			send := func(kind string) {
				t.Helper()
				if err := e.Handle(ctx, host.Client, randomizerEvent(kind, "key", "dice", settings)); err != nil {
					t.Fatal(err)
				}
			}
			send("willAppear")
			send("keyDown")
			clock.Advance(rollDuration)
			if err := e.Poll(ctx, host.Client); err != nil {
				t.Fatal(err)
			}
			if !e.NextPoll(clock.Now()).IsZero() || len(e.rolls) != 0 {
				t.Fatal("disabled badge retained reveal overhead")
			}
			view := e.Last["key"]
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(view.Image, "data:image/svg+xml;base64,"))
			if err != nil || strings.Contains(string(raw), "data-result-badge") || strings.Contains(string(raw), `fill="#0f141d"`) ||
				view.Feedback["line0"].(map[string]any)["value"] != "" || view.Title != fmt.Sprintf("d%d", sides) {
				t.Fatal("disabled badge or result overlay remained visible", err)
			}
			settings["show_result_badge"] = true
			send("didReceiveSettings")
			send("keyDown")
			clock.Advance(500 * time.Millisecond)
			settings["show_result_badge"] = false
			send("didReceiveSettings")
			if len(e.rolls) != 0 {
				t.Fatal("settings left stale roll")
			}
			send("keyDown")
			clock.Advance(rollDuration)
			if err := e.Poll(ctx, host.Client); err != nil {
				t.Fatal(err)
			}
			if len(e.rolls) != 0 {
				t.Fatal("reroll did not use saved preference")
			}
		})
	}
}
