// Package hiscores shares snapshot and baseline mechanics, while each game owns
// its board endpoints and mapping. No companion client or live XP inference
package hiscores

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Item struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Rank  *int64 `json:"rank"`
	Level *int64 `json:"level"`
	XP    *int64 `json:"xp"`
	Score *int64 `json:"score"`
}

// UnmarshalJSON decodes the external value into the supported representation
func (i *Item) UnmarshalJSON(data []byte) error {
	type plain Item
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	raw, ok := keys["id"]
	if !ok || string(raw) == "null" {
		return fmt.Errorf("Hiscores ID missing")
	}
	*i = Item(v)
	return nil
}

type Sample struct {
	Skills, Activities map[string]Item
	Delta              map[string]int64
	At                 time.Time
	Player             string
}
type Mapping struct {
	Skills, Activities []string
	ActivityIDs        []int
}
type Driver struct {
	Game           *Game
	HTTP           *c.HTTP
	Boards         map[string]string
	Mapping        *Mapping
	FallbackBoards map[string]string
	Latest         map[string][]skillXP
	Baseline       map[string][]skillXP
	historyBytes   int
	Now            func() time.Time
}

// New constructs the fixture or runtime instance with its dependencies
func New(boards map[string]string, mapping *Mapping) *Driver {
	return &Driver{HTTP: c.NewHTTP(), Boards: boards, Mapping: mapping, Latest: map[string][]skillXP{}, Baseline: map[string][]skillXP{}, Now: time.Now}
}

// Key derives the identity used to share service observations
func (*Driver) Key(s map[string]any) string {
	return strings.ToLower(playerName(s)) + ":" + c.String(s, "board", "normal")
}

// playerName validates and normalizes the requested player name
func playerName(s map[string]any) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' {
			return ' '
		}
		return r
	}, c.String(s, "username", "")))
}

// Failure keeps a short device label and a full inspector explanation, while
// retaining HTTP status/Retry-After through connector credential sanitization
type Failure struct {
	Label, Detail string
	Cause         error
}

// Error returns the caller-facing failure message
func (e *Failure) Error() string { return e.Label + "\n" + e.Detail }

// Unwrap exposes the underlying failure for error inspection
func (e *Failure) Unwrap() error { return e.Cause }

// SafeError returns an error description safe for display
func (e *Failure) SafeError() error { return &Failure{e.Label, e.Detail, e.Cause} }

var username = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,12}$`)

// Read reads the requested data through the owned adapter
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	player := playerName(s)
	if player == "" {
		return nil, &Failure{Label: "Set player", Detail: "Enter a player name in Action settings, apply settings, then save the binding."}
	}
	if !username.MatchString(player) {
		return nil, &Failure{Label: "Check name", Detail: "Use the in-game player name: 1–12 letters, numbers, spaces or hyphens. Account email addresses are not player names."}
	}
	endpoint := d.Boards[c.String(s, "board", "normal")]
	if endpoint == "" {
		return nil, &Failure{Label: "Check board", Detail: "Choose a supported Hiscores board in Action settings."}
	}
	key := d.Key(s)
	if _, known := d.Baseline[key]; !known && len(d.Baseline) >= historyKeyLimit {
		return nil, historyFull()
	}
	endpoint += "?player=" + url.QueryEscape(player)
	data, err := d.HTTP.Do(ctx, "GET", endpoint, nil, nil)
	csvResponse := false
	if failure, ok := err.(*c.HTTPError); ok && failure.Status == 404 && d.Mapping != nil {
		if fallback := d.FallbackBoards[c.String(s, "board", "normal")]; fallback != "" {
			data, err = d.HTTP.Do(ctx, "GET", fallback+"?player="+url.QueryEscape(player), nil, nil)
			csvResponse = true
		}
	}
	if err != nil {
		var httpError *c.HTTPError
		if errors.As(err, &httpError) && httpError.Status == 404 {
			return nil, &Failure{Label: "Player not found", Detail: "This player is not listed on the selected game's Hiscores board. Check the player name, game and board.", Cause: err}
		}
		return nil, &Failure{Label: "Retry later", Detail: "Hiscores could not be fetched. The service may be offline or rate limited; automatic refresh will retry.", Cause: err}
	}
	var sample Sample
	if !csvResponse {
		sample, err = ParseJSON(data)
	} else {
		sample, err = ParseCSV(data, *d.Mapping)
	}
	if err != nil {
		return nil, &Failure{Label: "Data unavailable", Detail: "The Hiscores response could not be read: " + err.Error(), Cause: err}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	sample.At = d.Now()
	sample.Player = player
	sample.Delta = map[string]int64{}
	baseline, err := d.retainXP(key, sample)
	if err != nil {
		return nil, err
	}
	for id, item := range sample.Skills {
		prior, ok := priorXP(baseline, item.ID)
		if ok && item.XP != nil && *item.XP >= 0 {
			sample.Delta[id] = *item.XP - prior
		}
	}
	return sample, nil
}

// ParseJSON decodes the bounded hiscores JSON response
func ParseJSON(data []byte) (Sample, error) {
	var payload struct {
		Skills     []Item `json:"skills"`
		Activities []Item `json:"activities"`
	}
	sample := Sample{Skills: map[string]Item{}, Activities: map[string]Item{}}
	if json.Unmarshal(data, &payload) != nil || len(payload.Skills) == 0 {
		return sample, fmt.Errorf("malformed ID-based Hiscores sample")
	}
	for _, group := range []struct {
		items []Item
		dest  map[string]Item
		skill bool
	}{{payload.Skills, sample.Skills, true}, {payload.Activities, sample.Activities, false}} {
		for _, item := range group.items {
			id := strconv.Itoa(item.ID)
			if _, ok := group.dest[id]; ok {
				return sample, fmt.Errorf("duplicate Hiscores ID")
			}
			if item.ID < 0 || item.Name == "" || item.Rank == nil || *item.Rank < -1 {
				return sample, fmt.Errorf("malformed Hiscores entry")
			}
			if group.skill {
				if item.Level == nil || item.XP == nil || *item.Level < -1 || *item.XP < -1 {
					return sample, fmt.Errorf("missing or invalid skill values")
				}
			} else if item.Score == nil || *item.Score < -1 {
				return sample, fmt.Errorf("missing or invalid activity score")
			}
			group.dest[id] = item
		}
	}
	if sample.Skills["0"].Name != "Overall" {
		return sample, fmt.Errorf("Hiscores sample lacks Overall ID 0")
	}
	return sample, nil
}

// ParseCSV decodes the ordered hiscores CSV response
func ParseCSV(data []byte, m Mapping) (Sample, error) {
	sample := Sample{Skills: map[string]Item{}, Activities: map[string]Item{}}
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1
	if len(m.ActivityIDs) > 0 && len(m.ActivityIDs) != len(m.Activities) {
		return sample, fmt.Errorf("invalid maintained RuneScape mapping")
	}
	rows, err := reader.ReadAll()
	if err != nil || len(rows) != len(m.Skills)+len(m.Activities) {
		return sample, fmt.Errorf("RuneScape Hiscores mapping changed; sample rejected")
	}
	for i, row := range rows {
		skill := i < len(m.Skills)
		count := 2
		if skill {
			count = 3
		}
		if len(row) != count {
			return sample, fmt.Errorf("RuneScape Hiscores row shape changed")
		}
		numbers := make([]int64, len(row))
		for j, raw := range row {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < -1 {
				return sample, fmt.Errorf("malformed RuneScape Hiscores integer")
			}
			numbers[j] = n
		}
		rank := numbers[0]
		item := Item{Rank: &rank}
		if skill {
			level, xp := numbers[1], numbers[2]
			item.ID = i
			item.Name = m.Skills[i]
			item.Level = &level
			item.XP = &xp
			sample.Skills[strconv.Itoa(i)] = item
		} else {
			score := numbers[1]
			item.ID = i - len(m.Skills)
			item.Name = m.Activities[item.ID]
			if len(m.ActivityIDs) > 0 {
				item.ID = m.ActivityIDs[item.ID]
			}
			item.Score = &score
			sample.Activities[strconv.Itoa(item.ID)] = item
		}
	}
	return sample, nil
}

// Command dispatches the requested command through the adapter
func (d *Driver) Command(_ context.Context, a string, s map[string]any, _ int) (c.Outcome, error) {
	if a == "reset-baseline" {
		key := d.Key(s)
		latest, ok := d.Latest[key]
		if !ok {
			return c.Outcome{}, fmt.Errorf("fetch a successful sample before resetting the baseline")
		}
		d.historyBytes += historyBytes(key, latest, latest) - historyBytes(key, d.Baseline[key], latest)
		d.Baseline[key] = latest
		return c.Outcome{Message: "Baseline reset to latest fetched snapshot"}, nil
	}
	return c.Outcome{Message: "Hiscores refresh requested"}, nil
}

// Discover returns the available inspector choices
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	value, err := d.Read(ctx, s)
	if err != nil {
		return nil, err
	}
	sample := value.(Sample)
	var out []c.Choice
	for id, item := range sample.Skills {
		out = append(out, c.Choice{ID: id, Name: item.Name, Kind: "skill"})
	}
	for id, item := range sample.Activities {
		out = append(out, c.Choice{ID: id, Name: item.Name, Kind: "activity"})
	}
	return out, nil
}

// Display builds the action display from its latest observation
func (d *Driver) Display(value any, a string, s map[string]any) string {
	sample := value.(Sample)
	id := c.String(s, "skill", "0")
	if id == "" {
		id = "0"
	}
	if a == "overall" {
		id = "0"
	}
	if a == "activity" {
		item, ok := sample.Activities[c.String(s, "activity", "0")]
		if !ok {
			return "Activity unavailable"
		}
		return item.Name + "\nScore " + number(item.Score) + " · rank " + number(item.Rank)
	}
	item, ok := sample.Skills[id]
	if !ok {
		return "Skill unavailable"
	}
	name := item.Name
	level := number(item.Level)
	if d.Game != nil {
		if skill, known := d.Game.Skill(id); known {
			name = skill.Name
			if item.Level != nil && *item.Level >= 0 {
				level += "/" + strconv.Itoa(skill.Cap)
			}
		}
	}
	if a == "xp-change" || a == "reset-baseline" {
		delta, ok := sample.Delta[id]
		if !ok {
			return name + "\nXP Δ unavailable"
		}
		return fmt.Sprintf("%s\n%+d\nFetched XP Δ", name, delta)
	}
	metric := c.String(s, "metric", "level")
	if a == "overall" || a == "refresh" {
		metric = "level"
	}
	switch metric {
	case "level":
		return name + "\n" + level
	case "xp":
		return name + "\n" + number(item.XP) + "\nExperience"
	case "rank":
		return name + "\n" + number(item.Rank) + "\nRank"
	}
	return fmt.Sprintf("%s\n%s\nXP %s\nRank %s", name, level, number(item.XP), number(item.Rank))
}

// number converts the external numeric field to its supported representation
func number(n *int64) string {
	if n == nil {
		return "Unavailable"
	}
	if *n == -1 {
		return "Unranked"
	}
	return strconv.FormatInt(*n, 10)
}

// Close releases the owned resources and pending work
func (d *Driver) Close() { d.HTTP.Close() }
