package vlc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var Icons = map[string]string{"toggle": "play|pause", "stop": "stop", "next": "next", "previous": "previous", "playlist": "list", "audio-track": "speaker", "subtitle-track": "list", "volume": "speaker", "seek": "latency", "track-info": "list", "progress": "latency", "volume-display": "speaker"}
var Definition, visuals = c.Define("vlc", "VLC", Icons, "toggle", "stop", "next", "previous", "playlist", "audio-track", "subtitle-track", "volume", "seek", "track-info", "progress", "volume-display")

// Declare the VLC global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Text("endpoint", "VLC HTTP address", "http://127.0.0.1:8080"), metadata.Sensitive("password", "VLC HTTP password")}
}

// Declare settings supported by the selected VLC action
func ActionFields(a string) []settings.Field {
	switch a {
	case "playlist":
		return []settings.Field{metadata.Device("playlist_item", "Playlist item")}
	case "audio-track":
		return []settings.Field{metadata.Device("audio_track", "Audio stream ID")}
	case "subtitle-track":
		return []settings.Field{metadata.Device("subtitle_track", "Subtitle stream ID (-1 disables)")}
	case "volume":
		return []settings.Field{metadata.Numeric("step", "Volume percent per tick", 1, 1, 100)}
	case "seek":
		return []settings.Field{metadata.Numeric("step", "Seconds per tick", 5, 0.1, 3600)}
	}
	return nil
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 5 * time.Second, Tick: time.Second, Dial: true, DialActions: engine.AdditiveActions(Definition), New: func(chan<- struct{}) c.Driver { return &Driver{HTTP: c.NewHTTP()} }})

type Status struct {
	Observed    time.Time `json:"-"`
	State       string    `json:"state"`
	Volume      *float64  `json:"volume"`
	Time        *float64  `json:"time"`
	Length      *float64  `json:"length"`
	Rate        *float64  `json:"rate"`
	Information struct {
		Category map[string]map[string]any `json:"category"`
	} `json:"information"`
}

// Decode the service response into supported typed status fields
func (status *Status) UnmarshalJSON(data []byte) error {
	type fields Status
	var wire struct {
		*fields
		Rate json.RawMessage `json:"rate"`
	}
	wire.fields = (*fields)(status)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	status.Rate = nil
	var rate float64
	if json.Unmarshal(wire.Rate, &rate) == nil && validRate(&rate) {
		status.Rate = &rate
	}
	return nil
}

type Driver struct {
	HTTP     *c.HTTP
	Now      func() time.Time
	cached   *Status
	identity [32]byte
}

// Choose the shared VLC resource identity for these settings
func (*Driver) Key(map[string]any) string { return "vlc" }

// Send a bounded VLC service request and decode its response
func (d *Driver) request(ctx context.Context, s map[string]any, path string, query url.Values, out any) error {
	endpoint, err := url.Parse(c.String(s, "endpoint", "http://127.0.0.1:8080"))
	if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("invalid VLC address")
	}
	password, err := c.Secret(s, "password")
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("configure VLC's HTTP password")
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/requests/" + path
	endpoint.RawQuery = query.Encode()
	return d.HTTP.JSON(ctx, "GET", endpoint.String(), map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+password))}, nil, out)
}

// Read the injected VLC clock or the current time
func (d *Driver) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Read the VLC resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	secret, err := c.Secret(s, "password")
	if err != nil {
		return nil, err
	}
	identity := sha256.Sum256([]byte(c.String(s, "endpoint", "http://127.0.0.1:8080") + "\x00" + secret))
	if d.cached != nil && identity == d.identity {
		cadence := 5 * time.Second
		if d.cached.State != "playing" {
			cadence = 30 * time.Second
		}
		if d.now().Sub(d.cached.Observed) < engine.RefreshInterval("vlc", "", s, cadence) {
			return *d.cached, nil
		}
	}
	value, err := d.readFresh(ctx, s)
	if err == nil {
		v := value.(Status)
		d.cached = &v
		d.identity = identity
	}
	return value, err
}

// Fetch VLC status without using the short-lived shared result
func (d *Driver) readFresh(ctx context.Context, s map[string]any) (any, error) {
	var v Status
	if err := d.request(ctx, s, "status.json", nil, &v); err != nil {
		return nil, err
	}
	if (v.State != "playing" && v.State != "paused" && v.State != "stopped") || v.Volume == nil || v.Time == nil || v.Length == nil || *v.Volume < 0 || *v.Time < 0 || *v.Length < 0 {
		return nil, fmt.Errorf("malformed VLC status")
	}
	v.Observed = d.now()
	return v, nil
}

// Execute the selected VLC command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, ticks int) (c.Outcome, error) {
	defer func() { d.cached = nil }()
	query := url.Values{}
	switch a {
	case "toggle":
		query.Set("command", "pl_pause")
	case "stop", "next", "previous":
		query.Set("command", "pl_"+a)
	case "playlist":
		id := c.String(s, "playlist_item", "")
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return c.Outcome{}, fmt.Errorf("select a discovered playlist item")
		}
		query.Set("command", "pl_play")
		query.Set("id", id)
	case "audio-track", "subtitle-track":
		key := strings.ReplaceAll(a, "-", "_")
		id := c.String(s, key, "")
		if _, err := strconv.ParseInt(id, 10, 32); err != nil {
			return c.Outcome{}, fmt.Errorf("select a discovered stream")
		}
		query.Set("command", key)
		query.Set("val", id)
	case "volume", "seek":
		if ticks == 0 {
			return c.Outcome{Message: "Rotate the dial to adjust " + a}, nil
		}
		current, err := d.readFresh(ctx, s)
		if err != nil {
			return c.Outcome{}, err
		}
		v := current.(Status)
		step := c.Number(s, "step", 1)
		if a == "seek" {
			step = c.Number(s, "step", 5)
		}
		if step <= 0 || step > 3600 {
			return c.Outcome{}, fmt.Errorf("invalid dial step")
		}
		query.Set("command", a)
		if a == "volume" {
			percent := c.Clamp(*v.Volume*100/256+float64(ticks)*step, 0, 100)
			query.Set("val", strconv.Itoa(int(percent*256/100+0.5)))
		} else {
			query.Set("val", strconv.FormatFloat(c.Clamp(*v.Time+float64(ticks)*step, 0, *v.Length), 'f', 2, 64))
		}
	case "track-info", "progress", "volume-display":
		_, err := d.readFresh(ctx, s)
		return c.Outcome{Message: "VLC refreshed"}, err
	default:
		return c.Outcome{}, fmt.Errorf("unknown VLC action")
	}
	var value json.RawMessage
	err := d.request(ctx, s, "status.json", query, &value)
	return c.Outcome{Message: "VLC command delivered; refreshing playback"}, err
}

// List VLC choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	var tree struct {
		Children []node `json:"children"`
	}
	if err := d.request(ctx, s, "playlist.json", nil, &tree); err != nil {
		return nil, err
	}
	var choices []c.Choice
	var walk func([]node)
	walk = func(nodes []node) {
		for _, n := range nodes {
			if len(n.Children) > 0 {
				walk(n.Children)
			} else if n.ID != "" {
				choices = append(choices, c.Choice{ID: n.ID, Name: n.Name, Kind: "playlist_item"})
			}
		}
	}
	walk(tree.Children)
	value, err := d.Read(ctx, s)
	if err != nil {
		return nil, err
	}
	for key, info := range value.(Status).Information.Category {
		if !strings.HasPrefix(key, "Stream ") {
			continue
		}
		id := strings.TrimPrefix(key, "Stream ")
		kind := ""
		switch fmt.Sprint(info["Type"]) {
		case "Audio":
			kind = "audio_track"
		case "Subtitle":
			kind = "subtitle_track"
		}
		if kind != "" {
			name := fmt.Sprint(info["Description"])
			if name == "<nil>" {
				name = key
			}
			choices = append(choices, c.Choice{ID: id, Name: name, Kind: kind})
		}
	}
	choices = append(choices, c.Choice{ID: "-1", Name: "Disable subtitles", Kind: "subtitle_track"})
	sort.Slice(choices, func(i, j int) bool { return choices[i].Kind+choices[i].ID < choices[j].Kind+choices[j].ID })
	return choices, nil
}

type node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Children []node `json:"children"`
}

// Format a VLC resource sample for its selected action
func (d *Driver) Display(value any, a string, _ map[string]any) string {
	v := value.(Status)
	switch a {
	case "volume", "volume-display":
		return fmt.Sprintf("VLC volume\n%.0f%%", *v.Volume*100/256)
	case "seek", "progress":
		elapsed := *v.Time
		if v.State == "playing" && !v.Observed.IsZero() && validRate(v.Rate) {
			elapsed = c.Clamp(elapsed+max(0, d.now().Sub(v.Observed).Seconds())**v.Rate, 0, *v.Length)
		}
		return fmt.Sprintf("%s\n%.0fs / %.0fs", v.State, elapsed, *v.Length)
	default:
		meta := v.Information.Category["meta"]
		title, ok := meta["title"].(string)
		if !ok {
			title, _ = meta["filename"].(string)
		}
		return "VLC · " + v.State + "\n" + title
	}
}

// Release the VLC driver resources during shutdown
func (d *Driver) Close() { d.HTTP.Close() }

// Choose the action icon state from observed VLC playback
func (*Driver) VisualState(value any, action string) int {
	if action == "toggle" && value.(Status).State == "playing" {
		return 1
	}
	return 0
}

// Validate a finite positive playback rate
func validRate(rate *float64) bool {
	return rate != nil && *rate > 0 && !math.IsNaN(*rate) && !math.IsInf(*rate, 0)
}
