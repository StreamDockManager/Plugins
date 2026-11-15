//go:build linux

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
	method := ""
	args := []string{}
	switch action {
	case "toggle":
		method = "PlayPause"
	case "next":
		method = "Next"
	case "previous":
		method = "Previous"
	case "seek":
		method = "Seek"
		seconds := configvalue.Number(s, "seconds", 10)
		if relative {
			out, e := process.Command(ctx, "gdbus", "call", "--session", "--dest", "org.mpris.MediaPlayer2.spotify", "--object-path", "/org/mpris/MediaPlayer2", "--method", "org.freedesktop.DBus.Properties.GetAll", "org.mpris.MediaPlayer2.Player")
			if e != nil {
				return "", e
			}
			status, e := parseSpotifyMPRIS(out)
			if e != nil {
				return "", e
			}
			args, e = mprisSeekArguments(status, delta)
			if e != nil {
				return "", e
			}
			method = "SetPosition"
		} else {
			args = []string{fmt.Sprintf("%d", int64(seconds*1e6))}
		}
	case "status":
		out, e := process.Command(ctx, "gdbus", "call", "--session", "--dest", "org.mpris.MediaPlayer2.spotify", "--object-path", "/org/mpris/MediaPlayer2", "--method", "org.freedesktop.DBus.Properties.GetAll", "org.mpris.MediaPlayer2.Player")
		if e != nil {
			return "", e
		}
		status, e := parseSpotifyMPRIS(out)
		if e != nil {
			return "", e
		}
		return spotifyStatusJSON(status)
	case "volume":
		v := configvalue.Number(s, "volume", 50) / 100
		if relative {
			out, e := process.Command(ctx, "gdbus", "call", "--session", "--dest", "org.mpris.MediaPlayer2.spotify", "--object-path", "/org/mpris/MediaPlayer2", "--method", "org.freedesktop.DBus.Properties.GetAll", "org.mpris.MediaPlayer2.Player")
			if e != nil {
				return "", e
			}
			current := variantFloat(out, "Volume")
			if current == nil {
				return "", fmt.Errorf("Spotify volume is unavailable")
			}
			v = min(1, max(0, *current+delta/100))
		}
		if v < 0 || v > 1 {
			return "", fmt.Errorf("volume must be 0..100")
		}
		return process.Command(ctx, "gdbus", "call", "--session", "--dest", "org.mpris.MediaPlayer2.spotify", "--object-path", "/org/mpris/MediaPlayer2", "--method", "org.freedesktop.DBus.Properties.Set", "org.mpris.MediaPlayer2.Player", "Volume", fmt.Sprintf("<%f>", v))
	default:
		return "", fmt.Errorf("Spotify action unavailable")
	}
	base := []string{"call", "--session", "--dest", "org.mpris.MediaPlayer2.spotify", "--object-path", "/org/mpris/MediaPlayer2", "--method", "org.mpris.MediaPlayer2.Player." + method}
	return process.Command(ctx, "gdbus", append(base, args...)...)
}
