package retroachievements

import (
	"context"
	"fmt"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Verify mode specific counts latest API game and recent unlocks
func TestModeSpecificCountsLatestAPIGameAndRecentUnlocks(t *testing.T) {
	requests := 0
	gameID := 12
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("y") != "fixture-secret" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/API_GetUserProfile.php":
			requests++
			fmt.Fprintf(w, `{"User":"fixture","LastGameID":%d,"TotalPoints":123,"TotalSoftcorePoints":456}`, gameID)
		case "/API_GetGameInfoAndUserProgress.php":
			if r.URL.Query().Get("g") != "12" {
				t.Error("wrong game")
			}
			fmt.Fprint(w, `{"ID":12,"Title":"Fixture game","NumAchievements":10,"NumAwardedToUser":8,"NumAwardedToUserHardcore":3}`)
		case "/API_GetUserRecentAchievements.php":
			fmt.Fprint(w, `[{"Title":"Hardcore unlock","GameTitle":"Fixture game","HardcoreMode":1},{"Title":"Softcore unlock","GameTitle":"Fixture game","HardcoreMode":0}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL + "/", Now: time.Now}
	defer d.Close()
	s := map[string]any{"username": "fixture", "api_key": "fixture-secret", "_action": "completion", "mode": "hardcore"}
	ctx := context.Background()
	sample, err := d.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if text := d.Display(sample, "completion", s); !strings.Contains(text, "Latest API game") || !strings.Contains(text, "hardcore 3/10") {
		t.Fatal(text)
	}
	s["mode"] = "softcore"
	if text := d.Display(sample, "completion", s); !strings.Contains(text, "softcore 8/10") {
		t.Fatal(text)
	}
	s["game_id"] = "12"
	sample, err = d.Read(ctx, s)
	if err != nil || strings.Contains(d.Display(sample, "completion", s), "Latest API game") {
		t.Fatal(sample, err)
	}
	s["_action"] = "points"
	sample, err = d.Read(ctx, s)
	if err != nil || !strings.Contains(d.Display(sample, "points", s), "456 points") {
		t.Fatal(sample, err)
	}
	s["_action"] = "recent"
	sample, err = d.Read(ctx, s)
	if err != nil || !strings.Contains(d.Display(sample, "recent", s), "Softcore unlock") {
		t.Fatal(sample, err)
	}
	s["mode"] = "hardcore"
	if text := d.Display(sample, "recent", s); !strings.Contains(text, "Hardcore unlock") {
		t.Fatal(text)
	}
	if requests != 1 {
		t.Fatal("profile requests not shared", requests)
	}
	gameID = 0
	d.profileAt = time.Time{}
	s["_action"], s["game_id"] = "completion", ""
	sample, err = d.Read(ctx, s)
	if err != nil || d.Display(sample, "completion", s) != "No latest game reported by API" {
		t.Fatal(sample, err)
	}
}

// Verify malformed profile and authentication
func TestMalformedProfileAndAuthentication(t *testing.T) {
	for _, status := range []int{200, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"User":"fixture"}`)
			}))
			defer server.Close()
			d := &Driver{HTTP: c.NewHTTP(), BaseURL: server.URL + "/", Now: time.Now}
			defer d.Close()
			if _, err := d.Read(context.Background(), map[string]any{"username": "fixture", "api_key": "secret"}); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
