package retroachievements

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"net/url"
	"strconv"
	"time"
)

var Icons = map[string]string{"points": "plus", "completion": "check", "recent": "list", "refresh": "cycle"}
var Definition, visuals = c.Define("retroachievements", "RetroAchievements", Icons, "points", "completion", "recent", "refresh")

// Declare the retroachievements global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Text("username", "RetroAchievements username or ULID", ""), metadata.Sensitive("api_key", "Web API key")}
}

// Declare settings supported by the selected retroachievements action
func ActionFields(a string) []settings.Field {
	f := []settings.Field{metadata.Select("mode", "Progress mode", "hardcore", "hardcore", "softcore")}
	if a == "completion" {
		f = append(f, metadata.Text("game_id", "Game ID (empty = latest game reported by API)", ""))
	}
	return f
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: time.Minute, New: func(chan<- struct{}) c.Driver {
	return &Driver{HTTP: c.NewHTTP(), BaseURL: "https://retroachievements.org/API/", Now: time.Now}
}})

type Profile struct {
	User                string
	LastGameID          *Count
	TotalPoints         *Count
	TotalSoftcorePoints *Count
}
type Progress struct {
	ID                       Count
	Title                    string
	NumAchievements          *Count
	NumAwardedToUser         *Count
	NumAwardedToUserHardcore *Count
}
type Unlock struct {
	Title         string
	GameTitle     string
	HardcoreMode  Count
	Date          string
	AchievementID int64
}

// Some legacy PHP responses encode integer fields as strings. Preserve integer
// precision and reject null/fractional/malformed values rather than coercing zero
type Count int64

// Decode the service response into supported typed status fields
func (c *Count) UnmarshalJSON(data []byte) error {
	var raw string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	} else {
		raw = string(data)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid RetroAchievements count")
	}
	*c = Count(n)
	return nil
}

type Sample struct {
	Profile  Profile
	Progress *Progress
	Unlocks  []Unlock
	Latest   bool
	NoGame   bool
}
type Driver struct {
	HTTP        *c.HTTP
	BaseURL     string
	Now         func() time.Time
	profile     Profile
	profileAt   time.Time
	profileUser string
}

// Choose the shared retroachievements resource identity for these settings
func (*Driver) Key(s map[string]any) string {
	a := c.String(s, "_action", "points")
	if a == "refresh" {
		a = "points"
	}
	if a == "completion" {
		return "completion:" + c.String(s, "game_id", "")
	}
	return a
}

// Send a bounded retroachievements service request and decode its response
func (d *Driver) request(ctx context.Context, s map[string]any, name string, query url.Values, out any) error {
	key, err := c.Secret(s, "api_key")
	if err != nil {
		return err
	}
	user := c.String(s, "username", "")
	if key == "" || user == "" {
		return fmt.Errorf("configure RetroAchievements username and web API key")
	}
	query.Set("y", key)
	query.Set("u", user)
	return d.HTTP.JSON(ctx, "GET", d.BaseURL+name+".php?"+query.Encode(), nil, nil, out)
}

// Read the retroachievements resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	user := c.String(s, "username", "")
	secret, err := c.Secret(s, "api_key")
	if err != nil {
		return nil, err
	}
	identity := fmt.Sprintf("%x", sha256.Sum256([]byte(user+"\x00"+secret)))
	if d.profileUser != identity || d.profileAt.IsZero() || d.Now().Sub(d.profileAt) >= engine.RefreshInterval("retroachievements", "", s, time.Minute) {
		var p Profile
		if err := d.request(ctx, s, "API_GetUserProfile", url.Values{}, &p); err != nil {
			return nil, err
		}
		if p.User == "" || p.TotalPoints == nil || p.TotalSoftcorePoints == nil || *p.TotalPoints < 0 || *p.TotalSoftcorePoints < 0 {
			return nil, fmt.Errorf("malformed RetroAchievements profile")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d.profile, d.profileAt, d.profileUser = p, d.Now(), identity
	}
	sample := Sample{Profile: d.profile}
	a := c.String(s, "_action", "points")
	if a == "completion" {
		id := c.String(s, "game_id", "")
		sample.Latest = id == ""
		if id == "" && d.profile.LastGameID != nil {
			id = strconv.FormatInt(int64(*d.profile.LastGameID), 10)
		}
		if id == "" || id == "0" {
			sample.NoGame = true
			return sample, nil
		}
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			return nil, fmt.Errorf("game ID must be a positive integer")
		}
		var p Progress
		if err := d.request(ctx, s, "API_GetGameInfoAndUserProgress", url.Values{"g": {id}}, &p); err != nil {
			return nil, err
		}
		if p.ID == 0 || p.Title == "" || p.NumAchievements == nil || p.NumAwardedToUser == nil || p.NumAwardedToUserHardcore == nil {
			return nil, fmt.Errorf("game progress unavailable or malformed")
		}
		if *p.NumAchievements < 0 || *p.NumAwardedToUser < 0 || *p.NumAwardedToUserHardcore < 0 || *p.NumAwardedToUser > *p.NumAchievements || *p.NumAwardedToUserHardcore > *p.NumAchievements {
			return nil, fmt.Errorf("invalid game progress counts")
		}
		sample.Progress = &p
	}
	if a == "recent" {
		if err := d.request(ctx, s, "API_GetUserRecentAchievements", url.Values{"m": {"60"}}, &sample.Unlocks); err != nil {
			return nil, err
		}
		for _, u := range sample.Unlocks {
			if u.Title == "" || (u.HardcoreMode != 0 && u.HardcoreMode != 1) {
				return nil, fmt.Errorf("malformed recent unlock")
			}
		}
	}
	return sample, nil
}

// Execute the selected retroachievements command and report its outcome
func (*Driver) Command(context.Context, string, map[string]any, int) (c.Outcome, error) {
	return c.Outcome{Message: "RetroAchievements refresh requested"}, nil
}

// List retroachievements choices for the property inspector
func (*Driver) Discover(context.Context, map[string]any) ([]c.Choice, error) { return nil, nil }

// Format a retroachievements resource sample for its selected action
func (*Driver) Display(value any, a string, s map[string]any) string {
	v := value.(Sample)
	mode := c.String(s, "mode", "hardcore")
	hc := mode == "hardcore"
	switch a {
	case "completion":
		if v.NoGame {
			return "No latest game reported by API"
		}
		if v.Progress == nil {
			return "Game progress unavailable"
		}
		p := v.Progress
		count := *p.NumAwardedToUser
		if hc {
			count = *p.NumAwardedToUserHardcore
		}
		label := p.Title
		if v.Latest {
			label = "Latest API game: " + label
		}
		return fmt.Sprintf("%s\n%s %d/%d", label, mode, count, *p.NumAchievements)
	case "recent":
		for _, u := range v.Unlocks {
			if (u.HardcoreMode == 1) == hc {
				return mode + " · recent unlock\n" + u.Title + " · " + u.GameTitle
			}
		}
		return "No " + mode + " unlocks in past 60 min"
	default:
		points := v.Profile.TotalSoftcorePoints
		if hc {
			points = v.Profile.TotalPoints
		}
		return fmt.Sprintf("%s · %s\n%d points", v.Profile.User, mode, *points)
	}
}

// Release the retroachievements driver resources during shutdown
func (d *Driver) Close() { d.HTTP.Close() }
