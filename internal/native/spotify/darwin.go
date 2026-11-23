//go:build darwin

package spotify

import (
	"context"
	"fmt"

	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

// Spotify reads or controls Spotify through the native media adapter
func Spotify(ctx context.Context, action string, s map[string]any) (string, error) {
	delta, relative, err := adjustment(s)
	if err != nil {
		return "", err
	}
	script := ""
	switch action {
	case "toggle":
		script = "playpause"
	case "next":
		script = "next track"
	case "previous":
		script = "previous track"
	case "volume":
		if relative {
			script = fmt.Sprintf("set sdVolume to (sound volume + (%f))\nif sdVolume < 0 then set sdVolume to 0\nif sdVolume > 100 then set sdVolume to 100\nset sound volume to sdVolume", delta)
			break
		}
		v := int(configvalue.Number(s, "volume", 50))
		if v < 0 || v > 100 {
			return "", fmt.Errorf("volume must be 0..100")
		}
		script = fmt.Sprintf("set sound volume to %d", v)
	case "seek":
		if relative {
			script = fmt.Sprintf("set sdPosition to (player position + (%f))\nif sdPosition < 0 then set sdPosition to 0\ntry\nset sdPosition to my minPosition(sdPosition, duration of current track / 1000)\nend try\nset player position to sdPosition", delta)
			break
		}
		script = fmt.Sprintf("set player position to %f", configvalue.Number(s, "position", 0))
	case "status":
		script = `set sdMode to player state as text
set sdPosition to ""
set sdDuration to ""
set sdArtwork to ""
set sdTitle to ""
set sdArtist to ""
set sdVolume to ""
try
set sdPosition to player position as text
end try
try
set sdDuration to (duration of current track / 1000) as text
end try
try
set sdArtwork to artwork url of current track
end try
try
set sdVolume to sound volume as text
end try
try
set sdTitle to name of current track
set sdArtist to artist of current track
end try
set sdSeparator to ASCII character 31
return sdMode & sdSeparator & sdPosition & sdSeparator & sdDuration & sdSeparator & sdArtwork & sdSeparator & sdVolume & sdSeparator & sdTitle & sdSeparator & sdArtist`
	default:
		return "", fmt.Errorf("Spotify action unavailable")
	}
	output, err := process.Command(ctx, "osascript", "-e", `on minPosition(value, maximum)
if value > maximum then return maximum
return value
end minPosition
if application "Spotify" is not running then error "Spotify is not running"
tell application "Spotify"`+"\n"+script+"\nend tell")
	if err != nil || action != "status" {
		return output, err
	}
	status, err := parseSpotifyAppleScript(output)
	if err != nil {
		return "", err
	}
	return spotifyStatusJSON(status)
}
