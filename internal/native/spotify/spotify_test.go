package spotify

import (
	"math"
	"testing"
)

// TestSpotifyNativeStatusUsesSecondsAndPlaybackState verifies spotify native status uses seconds and playback state
func TestSpotifyNativeStatusUsesSecondsAndPlaybackState(t *testing.T) {
	mac, err := parseSpotifyAppleScript("playing\n45,75\n214,56\nhttps://example.invalid/cover.png")
	if err != nil || mac.Mode != "playing" || *mac.PositionSeconds != 45.75 || *mac.DurationSeconds != 214.56 || mac.Artwork == "" {
		t.Fatal(mac, err)
	}
	stopped, err := parseSpotifyAppleScript("stopped")
	if err != nil || stopped.Mode != "stopped" || stopped.PositionSeconds != nil || stopped.DurationSeconds != nil || stopped.Artwork != "" {
		t.Fatal(stopped, err)
	}
	linux, err := parseSpotifyMPRIS(`({'PlaybackStatus': <'Paused'>, 'Metadata': <{'mpris:length': <int64 214560000>, 'mpris:artUrl': <'file:///tmp/artist\'s%20album.png'>}>, 'Position': <int64 45750000>},)`)
	if err != nil || linux.Mode != "paused" || *linux.PositionSeconds != 45.75 || *linux.DurationSeconds != 214.56 || linux.Artwork != "file:///tmp/artist's%20album.png" {
		t.Fatal(linux, err)
	}
	missing, err := parseSpotifyMPRIS(`({'PlaybackStatus': <'Stopped'>},)`)
	if err != nil || missing.PositionSeconds != nil || missing.DurationSeconds != nil || missing.Artwork != "" {
		t.Fatal(missing, err)
	}
	if _, err := parseSpotifyMPRIS(`({'Metadata': <{}>},)`); err == nil {
		t.Fatal("missing playback state was accepted")
	}
}

// TestSpotifyTrackMetadataAndVolume verifies spotify track metadata and volume
func TestSpotifyTrackMetadataAndVolume(t *testing.T) {
	mac, err := parseSpotifyAppleScript("playing\x1f45\x1f180\x1f\x1f37\x1fSong\nwith newline\x1fArtist")
	if err != nil || mac.Title != "Song\nwith newline" || mac.Artist != "Artist" || mac.VolumePercent == nil || *mac.VolumePercent != 37 {
		t.Fatal(mac, err)
	}
	linux, err := parseSpotifyMPRIS(`({'PlaybackStatus': <'Playing'>, 'Volume': <0.37>, 'Metadata': <{'xesam:title': <'Song'>, 'xesam:artist': <['First', 'Second\'s band']>}>},)`)
	if err != nil || linux.Title != "Song" || linux.Artist != "First, Second's band" || linux.VolumePercent == nil || *linux.VolumePercent != 37 {
		t.Fatal(linux, err)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), -1} {
		status := (SpotifyStatus{VolumePercent: &value}).Normalized()
		if status.VolumePercent != nil {
			t.Fatal("invalid volume retained", value)
		}
		if _, _, err := adjustment(map[string]any{"delta": value}); err == nil && value != -1 {
			t.Fatal("invalid delta retained", value)
		}
	}
}

// TestMPRISDialSeekClampsAndUsesCurrentTrackIdentity verifies MPRIS dial seek clamps and uses current track identity
func TestMPRISDialSeekClampsAndUsesCurrentTrackIdentity(t *testing.T) {
	status, err := parseSpotifyMPRIS(`({'PlaybackStatus': <'Playing'>, 'Position': <int64 45000000>, 'Metadata': <{'mpris:length': <int64 180000000>, 'mpris:trackid': <objectpath '/org/mpris/MediaPlayer2/Track/42'>}>},)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		delta  float64
		target string
	}{{-50, "0"}, {5, "50000000"}, {1000, "180000000"}} {
		args, err := mprisSeekArguments(status, test.delta)
		if err != nil || len(args) != 2 || args[0] != status.TrackID || args[1] != test.target {
			t.Fatal(args, err)
		}
	}
	status.TrackID = "invalid:track"
	if _, err := mprisSeekArguments(status, 5); err == nil {
		t.Fatal("invalid track accepted")
	}
}

// TestWindowsSpotifyTimelineAdvancesOnlyWhilePlaying verifies windows spotify timeline advances only while playing
func TestWindowsSpotifyTimelineAdvancesOnlyWhilePlaying(t *testing.T) {
	const second = int64(10000000)
	for _, sample := range []struct {
		mode string
		rate float64
		want float64
	}{
		{"playing", 1, 47}, {"paused", 1, 45}, {"stopped", 1, 45}, {"buffering", 1, 45}, {"playing", 2, 49},
	} {
		state := spotifyWindowsTimeline(sample.mode, 10*second, 224*second, 55*second, 100*second, 102*second, sample.rate)
		if *state.PositionSeconds != sample.want || *state.DurationSeconds != 214 {
			t.Fatal(sample, state)
		}
	}
	state := spotifyWindowsTimeline("playing", 0, 214*second, 213*second, 100*second, 103*second, 1)
	if *state.PositionSeconds != 214 {
		t.Fatal("position exceeded track duration", state)
	}
	for value, want := range map[int32]string{0: "stopped", 2: "buffering", 3: "stopped", 4: "playing", 5: "paused", 99: "unknown"} {
		if spotifyWindowsMode(value) != want {
			t.Fatal(value, want)
		}
	}
}

// TestSpotifyStatusRejectsInvalidTimingWithoutInventingZero verifies spotify status rejects invalid timing without inventing zero
func TestSpotifyStatusRejectsInvalidTimingWithoutInventingZero(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		status := (SpotifyStatus{PositionSeconds: &value, DurationSeconds: &value}).Normalized()
		if status.PositionSeconds != nil || status.DurationSeconds != nil {
			t.Fatal(status)
		}
	}
	zero := (SpotifyStatus{PositionSeconds: spotifySeconds(0)}).Normalized()
	if zero.PositionSeconds == nil || *zero.PositionSeconds != 0 {
		t.Fatal("valid track start was lost", zero)
	}
}

// TestSpotifyRateIsExplicitAndInvalidOrMissingRatesDoNotInventAdvancement verifies spotify rate is explicit and invalid or missing rates do not invent advancement
func TestSpotifyRateIsExplicitAndInvalidOrMissingRatesDoNotInventAdvancement(t *testing.T) {
	status, err := parseSpotifyMPRIS(`({'PlaybackStatus': <'Playing'>, 'Position': <int64 1000000>, 'Rate': <1.5>},)`)
	if err != nil || status.Rate == nil || *status.Rate != 1.5 {
		t.Fatal(status, err)
	}
	status, err = parseSpotifyMPRIS(`({'PlaybackStatus': <'Playing'>, 'Position': <int64 1000000>},)`)
	if err != nil || status.Rate != nil {
		t.Fatal("missing rate invented", status, err)
	}
	status = spotifyWindowsTimeline("playing", 0, 1000000000, 10000000, 10, 10000010, 0)
	if status.Rate != nil || status.PositionSeconds == nil || *status.PositionSeconds != 1 {
		t.Fatal("missing Windows rate advanced position", status)
	}
}
