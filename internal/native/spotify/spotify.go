package spotify

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type MediaObserver interface {
	Healthy() bool
	Revision() uint64
	Done() bool
	Close()
}

// Normalized normalizes media state for the shared display contract
func (s SpotifyStatus) Normalized() SpotifyStatus {
	s.Mode = strings.ToLower(strings.TrimSpace(s.Mode))
	switch s.Mode {
	case "playing", "paused", "stopped", "buffering", "unavailable":
	default:
		s.Mode = "unknown"
	}
	if s.PositionSeconds != nil {
		s.PositionSeconds = spotifySeconds(*s.PositionSeconds)
	}
	if s.DurationSeconds != nil {
		s.DurationSeconds = spotifySeconds(*s.DurationSeconds)
	}
	if s.Rate != nil && (math.IsNaN(*s.Rate) || math.IsInf(*s.Rate, 0) || *s.Rate == 0) {
		s.Rate = nil
	}
	if s.PositionSeconds != nil && s.DurationSeconds != nil && *s.PositionSeconds > *s.DurationSeconds {
		s.PositionSeconds = spotifySeconds(*s.DurationSeconds)
	}
	if s.VolumePercent != nil {
		v := *s.VolumePercent
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			s.VolumePercent = nil
		} else {
			v = min(100, v)
			s.VolumePercent = &v
		}
	}
	return s
}

// parseSpotifyAppleScript decodes the AppleScript media status response
func parseSpotifyAppleScript(output string) (SpotifyStatus, error) {
	lines := strings.Split(strings.TrimRight(output, "\r\n"), "\n")
	if strings.Contains(output, "\x1f") {
		lines = strings.Split(strings.TrimRight(output, "\r\n"), "\x1f")
	}
	if lines[0] == "" {
		return SpotifyStatus{}, fmt.Errorf("Spotify returned no playback state")
	}
	status := SpotifyStatus{Mode: lines[0]}
	for index, target := range map[int]**float64{1: &status.PositionSeconds, 2: &status.DurationSeconds} {
		if len(lines) > index {
			value, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(lines[index]), ",", "."), 64)
			if err == nil {
				*target = spotifySeconds(value)
			}
		}
	}
	if len(lines) > 3 {
		status.Artwork = strings.TrimSpace(lines[3])
	}
	if len(lines) > 4 {
		if value, err := strconv.ParseFloat(strings.TrimSpace(lines[4]), 64); err == nil {
			status.VolumePercent = &value
		}
	}
	if len(lines) > 5 {
		status.Title = strings.TrimSpace(lines[5])
	}
	if len(lines) > 6 {
		status.Artist = strings.TrimSpace(lines[6])
	}
	// Spotify reports normal-speed playback through its AppleScript dictionary
	rate := 1.0
	status.Rate = &rate
	return status.Normalized(), nil
}

// parseSpotifyMPRIS decodes the MPRIS media status response
func parseSpotifyMPRIS(output string) (SpotifyStatus, error) {
	mode := variantString(output, "PlaybackStatus")
	if mode == "" {
		return SpotifyStatus{}, fmt.Errorf("Spotify MPRIS playback state is unavailable")
	}
	volume := variantFloat(output, "Volume")
	if volume != nil {
		v := *volume * 100
		volume = &v
	}
	return (SpotifyStatus{
		Mode: mode, PositionSeconds: variantNumber(output, "Position"),
		DurationSeconds: variantNumber(output, "mpris:length"), Artwork: variantString(output, "mpris:artUrl"),
		Rate:          variantFloat(output, "Rate"),
		VolumePercent: volume, Title: variantString(output, "xesam:title"), Artist: variantStringArray(output, "xesam:artist"),
		TrackID: variantString(output, "mpris:trackid"),
	}).Normalized(), nil
}

// spotifySeconds converts the media timestamp to seconds
func spotifySeconds(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	return &value
}

// spotifyStatusJSON serializes the normalized media status
func spotifyStatusJSON(status SpotifyStatus) (string, error) {
	data, err := json.Marshal(status.Normalized())
	return string(data), err
}

// spotifyWindowsMode maps Windows playback status to the shared mode
func spotifyWindowsMode(value int32) string {
	switch value {
	case 0, 1, 3:
		return "stopped"
	case 2:
		return "buffering"
	case 4:
		return "playing"
	case 5:
		return "paused"
	default:
		return "unknown"
	}
}

// spotifyWindowsTimeline converts Windows timeline data to the shared position
func spotifyWindowsTimeline(mode string, start, end, position, updated, now int64, rate float64) SpotifyStatus {
	s := SpotifyStatus{Mode: mode, PositionSeconds: spotifySeconds(float64(position-start) / 1e7), Rate: &rate}
	if end > start {
		s.DurationSeconds = spotifySeconds(float64(end-start) / 1e7)
	}
	if mode == "playing" && updated > 0 && now >= updated && s.PositionSeconds != nil && rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
		s.PositionSeconds = spotifySeconds(*s.PositionSeconds + float64(now-updated)/1e7*rate)
	}
	return s.Normalized()
}

type SpotifyStatus struct {
	Mode            string   `json:"mode"`
	PositionSeconds *float64 `json:"position_seconds"`
	DurationSeconds *float64 `json:"duration_seconds"`
	Artwork         string   `json:"artwork"`
	Rate            *float64 `json:"rate,omitempty"`
	VolumePercent   *float64 `json:"volume_percent,omitempty"`
	Title           string   `json:"title,omitempty"`
	Artist          string   `json:"artist,omitempty"`
	TrackID         string   `json:"-"`
}

var mprisTrackPath = regexp.MustCompile(`^/(?:[A-Za-z0-9_]+(?:/[A-Za-z0-9_]+)*)?$`)

// mprisSeekArguments builds the bounded MPRIS seek arguments
func mprisSeekArguments(status SpotifyStatus, delta float64) ([]string, error) {
	status = status.Normalized()
	if status.PositionSeconds == nil || !mprisTrackPath.MatchString(status.TrackID) || status.TrackID == "/org/mpris/MediaPlayer2/TrackList/NoTrack" {
		return nil, fmt.Errorf("Spotify track position is unavailable")
	}
	target := max(0, *status.PositionSeconds+delta)
	if status.DurationSeconds != nil {
		target = min(target, *status.DurationSeconds)
	}
	if math.IsNaN(target) || math.IsInf(target, 0) || target >= float64(math.MaxInt64)/1e6 {
		return nil, fmt.Errorf("Spotify position exceeds the native limit")
	}
	// SetPosition includes the track identity, so a concurrent track change is
	// ignored and reaching the end never becomes an implicit Next operation
	return []string{status.TrackID, strconv.FormatInt(int64(target*1e6), 10)}, nil
}

// Relative operations are bounded before formatting values into native calls
func adjustment(s map[string]any) (float64, bool, error) {
	raw, ok := s["delta"]
	if !ok {
		return 0, false, nil
	}
	value, ok := raw.(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 86400 {
		return 0, true, fmt.Errorf("invalid Spotify dial delta")
	}
	return value, true, nil
}

// variantStringArray extracts a string list from a D-Bus variant
func variantStringArray(output, key string) string {
	pattern := `['"]` + regexp.QuoteMeta(key) + `['"]:\s*<\s*\[([^\]]*)\]`
	match := regexp.MustCompile(pattern).FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	quoted := regexp.MustCompile(`'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"`).FindAllString(match[1], -1)
	values := []string{}
	for _, value := range quoted {
		text := variantString(`'value': <`+value+`>`, "value")
		if text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, ", ")
}

// variantFloat extracts a floating-point value from a D-Bus variant
func variantFloat(output, key string) *float64 {
	pattern := `['"]` + regexp.QuoteMeta(key) + `['"]:\s*<\s*(?:u?int(?:32|64)\s+)?(-?[0-9]+(?:\.[0-9]+)?)\s*>`
	match := regexp.MustCompile(pattern).FindStringSubmatch(output)
	if len(match) != 2 {
		return nil
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return nil
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// variantNumber extracts a numeric value from a D-Bus variant
func variantNumber(output, key string) *float64 {
	value := variantFloat(output, key)
	if value == nil {
		return nil
	}
	return spotifySeconds(*value / 1e6)
}

// variantString extracts a string from a D-Bus variant
func variantString(output, key string) string {
	pattern := `['"]` + regexp.QuoteMeta(key) + `['"]:\s*<\s*(?:objectpath\s+)?('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*")`
	match := regexp.MustCompile(pattern).FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	quoted := match[1]
	remaining := quoted[1 : len(quoted)-1]
	var result strings.Builder
	for remaining != "" {
		value, _, tail, err := strconv.UnquoteChar(remaining, quoted[0])
		if err != nil {
			return ""
		}
		result.WriteRune(value)
		remaining = tail
	}
	return result.String()
}
