package engine

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/polling"
	"time"
)

// RefreshPolicy is shared by packaged metadata and the product samplers
func RefreshPolicy(slug, action string) *polling.RefreshPolicy {
	var seconds int64
	switch slug {
	case "weather":
		seconds = 3600
	case "system-monitor":
		seconds = 1
		if action == "disk" {
			seconds = 5
		}
	case "battery-power", "microphone-controls", "network-vpn", "spotify":
		seconds = 1
	case "vlc", "tailscale":
		seconds = 5
	case "osrs-hiscores", "runescape-hiscores":
		seconds = 300
	case "home-assistant", "mqtt", "retroachievements":
		seconds = 60
	case "obs-studio", "make":
		seconds = 30
	case "http-webhooks":
		if action == "cancel" {
			return nil
		}
		seconds = 0
	case "service-monitor":
		if action == "cancel" || action == "refresh" {
			return nil
		}
		seconds = 30
	default:
		return nil
	}
	return &polling.RefreshPolicy{DefaultMS: seconds * 1000, MinimumMS: 1000, MaximumMS: 86400000}
}

// RefreshInterval resolves the polling interval from the action settings
func RefreshInterval(slug, action string, settings map[string]any, fallback time.Duration) time.Duration {
	if RefreshPolicy(slug, action) == nil {
		return fallback
	}
	return polling.Interval(settings, fallback)
}

// RefreshNext calculates the next permitted refresh deadline
func RefreshNext(now time.Time, interval time.Duration) time.Time { return polling.Next(now, interval) }
