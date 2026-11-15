package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugins/internal/hiscores"
	osrs "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	rs "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
)

// Opt-in smoke test of the actual Go HTTP client/parser; ordinary tests remain
// deterministic and offline. Public Hiscores require no game credentials
func TestLiveHiscoresPublicPlayers(t *testing.T) {
	if os.Getenv("STREAM_DOCK_MANAGER_HISCORES_LIVE") != "1" {
		t.Skip("public network smoke test is opt-in")
	}
	for _, tc := range []struct {
		game             hiscores.Game
		boards, fallback map[string]string
		mapping          *hiscores.Mapping
		player           string
	}{
		{osrs.Game, osrs.Boards, nil, nil, "Echolocate"},
		{rs.Game, rs.Boards, rs.LegacyBoards, &rs.Mapping, "Zezima"},
	} {
		t.Run(tc.game.Slug, func(t *testing.T) {
			d := hiscores.New(tc.boards, tc.mapping)
			defer d.Close()
			d.Game = &tc.game
			d.FallbackBoards = tc.fallback
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			s := map[string]any{"username": tc.player, "board": "normal", "skill": "1"}
			value, err := d.Read(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if len(value.(hiscores.Sample).Skills) != len(tc.game.Skills) {
				t.Fatal("provider skill inventory changed")
			}
			t.Log(d.Display(value, "skill", s))
		})
	}
}
