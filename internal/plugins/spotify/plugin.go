package spotify

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/httptrack"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/cache"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	sampleartwork "github.com/StreamDockManager/Plugin-SDK/Go/thumbnail"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/native/spotify"
)

type Plugin struct {
	spotifyVolumeMu          sync.Mutex
	spotifyRestoreVolume     float64
	spotifyDelivery          map[string]time.Time
	spotifyDeliveredRevision uint64
	spotifyDefaultInterval   time.Duration
	*engine.Engine
	telemetryExpiry      time.Time
	State                map[string]*saved
	artCache             *cache.Cache[string]
	spotify              func(context.Context, string, map[string]any) (string, error)
	spotifyNext          time.Time
	spotifyObserved      time.Time
	spotifySample        spotify.SpotifyStatus
	spotifyFailures      int
	spotifyArt           bool
	spotifyObserver      spotify.MediaObserver
	observeSpotify       func(chan<- struct{}) spotify.MediaObserver
	spotifyRevision      uint64
	spotifyObserverRetry time.Time
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type pluginDisplay = engine.Display

type saved struct {
	Value       float64 `json:"value"`
	Deadline    int64   `json:"deadline"`
	Mode        string  `json:"mode"`
	ArtworkOnly bool    `json:"artwork_only,omitempty"`
}

type spotifyDisplay = pluginDisplay

type spotifyProbe struct {
	Status          spotify.SpotifyStatus
	ArtKey, Artwork string
	Variants        map[string]string
	At              time.Time
	Revision        uint64
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

// Attach Spotify lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Prepare bounded artwork for a device display
func artwork(ctx context.Context, source string) (string, error) {
	data, err := readArtwork(ctx, source)
	if err != nil {
		return "", err
	}
	result, err := sampleartwork.Render(ctx, data, 256, 256)
	return result.DataURI, err
}

func readArtwork(ctx context.Context, source string) ([]byte, error) {
	var data []byte
	var e error
	if strings.HasPrefix(source, "data:") {
		_, encoded, ok := strings.Cut(source, ",")
		if !ok || len(encoded) > base64.StdEncoding.EncodedLen(sampleartwork.MaxInputBytes) {
			return nil, fmt.Errorf("artwork exceeds limit")
		}
		data, e = base64.StdEncoding.DecodeString(encoded)
	} else {
		u, x := url.Parse(source)
		if x != nil {
			return nil, x
		}
		if u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") && filepath.IsAbs(u.Path) {
			var f *os.File
			f, e = os.Open(u.Path)
			if e == nil {
				defer f.Close()
				data, e = io.ReadAll(io.LimitReader(f, sampleartwork.MaxInputBytes+1))
			}
		} else if u.Scheme == "https" && u.User == nil {
			client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
				if len(via) > 4 || r.URL.Scheme != "https" {
					return fmt.Errorf("invalid artwork redirect")
				}
				return nil
			}}
			req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
			var r *http.Response
			r, e = httptrack.TrackHTTPClient(&client).Do(req)
			if e == nil {
				defer r.Body.Close()
				if r.StatusCode != 200 {
					return nil, fmt.Errorf("artwork HTTP %d", r.StatusCode)
				}
				data, e = io.ReadAll(io.LimitReader(r.Body, sampleartwork.MaxInputBytes+1))
			}
		} else {
			return nil, fmt.Errorf("artwork source unavailable")
		}
	}
	if e != nil {
		return nil, e
	}
	if len(data) > sampleartwork.MaxInputBytes {
		return nil, fmt.Errorf("artwork exceeds limit")
	}
	return data, nil
}

// A 256x256 RGBA PNG has at most about 350 KiB of base64 pixel data even
// without compression. 8 MiB accommodates 16 such outputs plus keys/overhead
// it is a logical retained-string budget, not a process-memory measurement
const artworkCacheBytes = 8 << 20

// Initialize Spotify state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	artCache, err := cache.New[string](16, artworkCacheBytes)
	if err != nil {
		panic(err) // Fixed, validated product limits
	}
	e := &Plugin{artCache: artCache, Engine: base, State: map[string]*saved{}, spotify: spotify.Spotify, observeSpotify: spotify.ObserveSpotify}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	return e
}

// Choose the later of two scheduling deadlines
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Apply Spotify follow-up work after the common event handler
func (e *Plugin) afterEvent(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	if ev.Event == "willAppear" || ev.Event == "willDisappear" || ev.Event == "didReceiveSettings" {
		e.syncSpotifyObserver(time.Now())
		return nil
	}
	return nil
}

// Apply completed Spotify work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {

	if result.ID == "spotify-status" {
		if err := e.applySpotify(ctx, c, result); err != nil {
			return err
		}
		return nil
	}

	text := result.Text
	if result.Err != nil {
		text = "Unavailable\n" + result.Err.Error()
	}
	e.ResultsText[result.ID] = text
	delete(e.Results, result.ID)

	destination := result.ID

	if err := engine.SendDiagnostic(ctx, c, destination, e.Bindings[destination].Action, result.Value); err != nil {
		return err
	}

	if _, ok := e.Bindings[result.ID]; ok {
		if err := e.Render(ctx, c, result.ID); err != nil {
			return err
		}
	}

	return nil
}

// Apply a completed Spotify observation and refresh playback views
func (e *Plugin) applySpotify(ctx context.Context, c *sdk.Client, result jobResult) error {
	probe, ok := result.Value.(spotifyProbe)
	if !ok {
		return nil
	}
	// Attribute a genuine shared observation failure to one visible action without flooding every key
	if e.Feature.ObserveFailure != nil && result.Err != nil {
		id := ""
		for candidate := range e.Bindings {
			if e.CanUpdateView(ctx, candidate) && (id == "" || candidate < id) {
				id = candidate
			}
		}
		if id != "" {
			e.Feature.ObserveFailure(ctx, c, id, result.Err)
		}
	}
	if result.Err == nil && e.Feature.FilterStatus != nil {
		for id := range e.Bindings {
			e.Feature.FilterStatus(ctx, c, id, displayStatus{})
		}
	}
	if result.Err != nil {
		e.spotifyFailures++
		probe.Status = spotify.SpotifyStatus{Mode: "unavailable"}
	} else {
		e.spotifyFailures = 0
	}
	interval := time.Second
	if e.spotifyObserver != nil && e.spotifyObserver.Healthy() {
		interval = 10 * time.Second
	}
	// WinRT playback notifications do not include Core Audio volume changes
	// Keep the shared sample fresh while a volume companion is visible
	for _, b := range e.Bindings {
		if action(b.Action) == "volume-display" || action(b.Action) == "volume" {
			interval = time.Second
			break
		}
	}
	if e.spotifyFailures > 0 {
		interval = min(60*time.Second, time.Second*time.Duration(1<<min(6, e.spotifyFailures-1)))
	}
	if probe.Revision != e.spotifyRevision {
		interval = min(interval, 250*time.Millisecond)
	}
	force := e.spotifyNext.IsZero() || probe.Revision != e.spotifyDeliveredRevision
	e.spotifyDeliveredRevision = probe.Revision
	e.spotifyDefaultInterval = interval
	if e.spotifyDelivery == nil {
		e.spotifyDelivery = map[string]time.Time{}
	}
	if force {
		clear(e.spotifyDelivery)
	}
	e.spotifyObserved = probe.At
	if e.spotifyObserved.IsZero() {
		e.spotifyObserved = time.Now()
	}
	if probe.ArtKey != "" {
		probe.Status.Artwork = probe.ArtKey
	}
	e.spotifySample = probe.Status
	for key, uri := range probe.Variants {
		_, _ = e.artCache.Put(key, uri, len(uri), 0, 0)
	}
	if probe.Artwork != "" {
		// Admission failure must not discard an otherwise valid display result
		_, _ = e.artCache.Put(probe.ArtKey, probe.Artwork, len(probe.Artwork), 0, 0)
	}

	for id := range e.Bindings {
		key, uri := probe.ArtKey, probe.Artwork
		if probe.Variants != nil {
			key = artworkVariant(key, artworkSize(e.Bindings[id]))
			uri = probe.Variants[key]
		}
		if err := e.renderSpotifyArtwork(ctx, c, id, probe.Status, key, uri); err != nil {
			return err
		}
	}
	e.spotifyNext = engine.RefreshNext(time.Now(), 0)
	for id, b := range e.Bindings {
		if !spotifyLive(action(b.Action)) {
			continue
		}
		due := e.spotifyDelivery[id]
		if _, override := b.Settings["update_interval_ms"]; !override {
			due = time.Now().Add(interval)
		}
		if due.Before(e.spotifyNext) {
			e.spotifyNext = due
		}
	}
	return nil
}

// Process Spotify metadata and lifecycle updates before common dispatch
func (e *Plugin) beforeEvent(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	if active(ev) || ev.Event == "dialRotate" || ev.Event == "willAppear" || ev.Event == "didReceiveSettings" {
		delete(e.spotifyDelivery, ev.Context)
	}
	// Switching the status view can reuse the current sample and in-flight cover
	if active(ev) && action(ev.Action) != "status" || ev.Event == "dialRotate" || ev.Event == "didReceiveSettings" {
		e.CancelJob("spotify-status")
		e.spotifyNext = time.Time{}
	}
	return false, nil
}

// Release Spotify work no longer needed by a disappearing binding
func (e *Plugin) unboundFeature(id string) {
	delete(e.spotifyDelivery, id)
	if len(e.Bindings) == 0 {
		e.CancelJob("spotify-status")
		e.spotifyNext = time.Time{}
		e.artCache.Clear()
		e.spotifySample = spotify.SpotifyStatus{}
		e.spotifyObserved = time.Time{}
	}
}

// Clear Spotify transient resources during feature shutdown
func (e *Plugin) closeFeature() {
	if e.spotifyObserver != nil {
		e.spotifyObserver.Close()
		e.spotifyObserver = nil
	}
	e.artCache.Clear()
}

// Build the current Spotify status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("spotify", action(b.Action), text, err)
}

// Route Spotify inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// dialAdjustment never changes persisted binding settings. Providers apply the
// signed delta to the current player value, not to a stale display sample
func dialAdjustment(a string, ev sdk.Event, settings map[string]any) (map[string]any, bool) {
	if ev.Event != "dialRotate" || (a != "volume" && a != "seek") {
		return nil, false
	}
	fallback := 1.0
	if a == "seek" {
		fallback = 5
	}
	step := configvalue.Number(settings, "step", fallback)
	maximum := 100.0
	if a == "seek" {
		maximum = 3600
	}
	if math.IsNaN(step) || math.IsInf(step, 0) || step <= 0 || step > maximum || ev.Data().Ticks == 0 {
		return nil, false
	}
	delta := float64(ev.Data().Ticks) * step
	if math.IsNaN(delta) || math.IsInf(delta, 0) || math.Abs(delta) > 86400 {
		return nil, false
	}
	return map[string]any{"delta": delta}, true
}

// Identify actions that display live Spotify playback state
func spotifyLive(a string) bool {
	switch a {
	case "mute-toggle", "toggle", "status", "volume", "seek", "volume-display", "progress", "track-info":
		return true
	}
	return false
}

// Volume commands and mute read/modify/write share one lock for the single local
// Spotify playback target. Restoration is session-local, never a binding setting.
func (e *Plugin) volumeCommand(ctx context.Context, action string, settings map[string]any) (string, error) {
	e.spotifyVolumeMu.Lock()
	defer e.spotifyVolumeMu.Unlock()
	return e.spotify(ctx, action, settings)
}

func (e *Plugin) toggleMute(ctx context.Context) error {
	e.spotifyVolumeMu.Lock()
	defer e.spotifyVolumeMu.Unlock()
	raw, err := e.spotify(ctx, "status", nil)
	if err != nil {
		return err
	}
	var status spotify.SpotifyStatus
	if err = json.Unmarshal([]byte(raw), &status); err != nil {
		return err
	}
	status = status.Normalized()
	if status.VolumePercent == nil {
		return fmt.Errorf("Spotify volume is unavailable")
	}
	current := *status.VolumePercent
	target := 0.0
	if current == 0 {
		target = e.spotifyRestoreVolume
		if target <= 0 {
			target = 50
		} // Explicit fallback when already muted before this session.
	}
	if _, err = e.spotify(ctx, "volume", map[string]any{"volume": target}); err != nil {
		return err
	}
	if current > 0 {
		e.spotifyRestoreVolume = current
	}
	return nil
}

// Dispatch an activating Spotify input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	a := action(b.Action)
	if a == "volume" || a == "seek" {
		values, ok := dialAdjustment(a, ev, b.Settings)
		if !ok {
			return nil
		}
		if _, err := e.volumeCommand(ctx, a, values); err != nil {
			return err
		}
	} else {
		if !active(ev) {
			return nil
		}
		// Status switches its own view; companions refresh without adjusting playback
		switch a {
		case "mute-toggle":
			if err := e.toggleMute(ctx); err != nil {
				return err
			}
		case "status":
			state := e.state(ev.Context, nil)
			state.ArtworkOnly = !state.ArtworkOnly
			if err := engine.RoutinePersist(e.Engine); err != nil {
				return err
			}
		case "volume-display", "progress", "track-info":
		case "toggle", "next", "previous":
			if _, err := e.spotify(ctx, a, b.Settings); err != nil {
				return err
			}
		default:
			return nil
		}
	}
	return e.Render(ctx, c, ev.Context)
}

// Expose the Spotify feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	classify := func(ev sdk.Event) bool {
		a := action(ev.Action)
		if a == "volume" || a == "seek" {
			return ev.Event == "dialRotate" && ev.Data().Ticks != 0
		}
		return active(ev) || ev.Event == "dialRotate" && ev.Data().Ticks != 0
	}
	return engine.Feature{ShouldLogAction: classify, AdditiveDial: func(a string) bool { return engine.AdditiveDial(Definition.Slug, a) }, Before: e.beforeEvent, After: e.afterEvent, Action: e.handleAction, Inspector: e.extensionInspector, Unbound: e.unboundFeature, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature, Render: func(ctx context.Context, c *sdk.Client, id string) error {
		// Status sampling belongs to Poll, outside the playback input's receipt
		// Rendering companion views must neither start nor duplicate shared work
		return e.renderSpotify(ctx, c, id, e.spotifyObservedStatus(time.Now()))
	}}
}

// Choose the next explicit Spotify deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	if len(e.Bindings) == 0 {
		e.telemetryExpiry = time.Time{}
		return e.PendingWrite
	}

	if e.NextPeriodic.IsZero() {
		e.NextPeriodic = now.Add(time.Second)
	}
	var deadline time.Time
	earlier := func(at time.Time) {
		if !at.IsZero() && (deadline.IsZero() || at.Before(deadline)) {
			deadline = at
		}
	}
	for _, b := range e.Bindings {
		{

			if !spotifyLive(action(b.Action)) {
				continue
			}
			if (action(b.Action) == "status" || action(b.Action) == "progress" || action(b.Action) == "seek") && e.spotifySample.Mode == "playing" && e.spotifySample.PositionSeconds != nil && e.spotifySample.Rate != nil {
				earlier(e.NextPeriodic)
			}
			if _, busy := e.Jobs["spotify-status"]; !busy {
				if e.spotifyNext.IsZero() {
					earlier(now)
				} else {
					earlier(e.spotifyNext)
				}
			}
		}

	}

	earlier(e.PendingWrite)
	return deadline
}

// Run due Spotify feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	return true, e.pollSpotify(ctx, c)
}

// Refresh Spotify observations and due playback displays
func (e *Plugin) pollSpotify(ctx context.Context, c *sdk.Client) error {
	e.syncSpotifyObserver(time.Now())
	if len(e.Bindings) == 0 {
		return nil
	}
	sizes := map[[2]int]bool{}
	includeArt := false
	for _, b := range e.Bindings {
		if action(b.Action) == "status" || action(b.Action) == "track-info" {
			includeArt = true
			sizes[artworkSize(b)] = true
		}
	}
	if includeArt != e.spotifyArt {
		e.CancelJob("spotify-status")
		e.spotifyNext = time.Time{}
		e.spotifyArt = includeArt
	}
	if _, busy := e.Jobs["spotify-status"]; !busy && e.spotifyDemand() && !time.Now().Before(e.spotifyNext) {
		probe := e.spotify
		cached := e.artworkSnapshot()
		revision := e.spotifyRevision
		e.startJob(ctx, "spotify-status", func(ctx context.Context) (string, any, error) {
			data, err := probe(ctx, "status", map[string]any{"include_artwork": includeArt})
			result := spotifyProbe{Status: spotify.SpotifyStatus{Mode: "unavailable"}, At: time.Now(), Revision: revision}
			if err != nil {
				return "", result, err
			}
			if err = json.Unmarshal([]byte(data), &result.Status); err != nil {
				return "", result, err
			}
			result.Status = result.Status.Normalized()
			if result.Status.Artwork != "" {
				result.ArtKey = fmt.Sprintf("spotify:%x", sha256.Sum256([]byte(result.Status.Artwork)))
				source := result.Status.Artwork
				result.Status.Artwork = result.ArtKey
				result.Variants = make(map[string]string, len(sizes))
				var data []byte
				for size := range sizes {
					key := artworkVariant(result.ArtKey, size)
					uri := cached[key]
					if uri == "" {
						if data == nil {
							data, err = readArtwork(ctx, source)
							if err != nil {
								break
							}
						}
						variant, err := sampleartwork.Render(ctx, data, size[0], size[1])
						if err != nil {
							continue
						}
						uri = variant.DataURI
					}
					result.Variants[key] = uri
				}
			}
			return "", result, nil
		})
	}
	status := e.spotifyObservedStatus(time.Now())
	for id := range e.Bindings {
		if err := e.renderSpotify(ctx, c, id, status); err != nil {
			return err
		}
	}
	return nil
}

// Copy immutable string values at the event-loop boundary; workers never touch
// the owner-serialized cache
func (e *Plugin) artworkSnapshot() map[string]string {
	entries, _ := e.artCache.Entries(0)
	snapshot := make(map[string]string, len(entries))
	for _, entry := range entries {
		snapshot[entry.Key] = entry.Value
	}
	return snapshot
}

func artworkSize(b binding) [2]int {
	if b.Surface == nil || b.Surface.Width <= 0 || b.Surface.Height <= 0 {
		return [2]int{256, 256}
	}
	return [2]int{min(b.Surface.Width, sampleartwork.MaxDimension), min(b.Surface.Height, sampleartwork.MaxDimension)}
}

func artworkVariant(key string, size [2]int) string {
	if size == [2]int{256, 256} {
		return key
	}
	return fmt.Sprintf("%s:%dx%d", key, size[0], size[1])
}

// Render the binding using the current Spotify status
func (e *Plugin) renderSpotify(ctx context.Context, c *sdk.Client, id string, status spotify.SpotifyStatus) error {
	return e.renderSpotifyArtwork(ctx, c, id, status, "", "")
}

// Render Spotify playback with the selected artwork snapshot
func (e *Plugin) renderSpotifyArtwork(ctx context.Context, c *sdk.Client, id string, status spotify.SpotifyStatus, artKey, artwork string) error {
	b := e.Bindings[id]
	if _, override := b.Settings["update_interval_ms"]; override && time.Now().Before(e.spotifyDelivery[id]) {
		return nil
	}
	view := PlaybackView(b, status)
	if state := e.State[id]; action(b.Action) == "status" && state != nil && state.ArtworkOnly {
		view = ArtworkView(b)
	}
	view.Layout = e.ThemedLayout(view.Layout)
	view.Image = e.ThemedAsset(view.Image)
	for _, key := range []string{"icon", "art"} {
		if value, ok := view.Feedback[key].(string); ok {
			view.Feedback[key] = e.ThemedAsset(value)
		} else if object, ok := view.Feedback[key].(map[string]any); ok {
			if value, ok := object["value"].(string); ok {
				object["value"] = e.ThemedAsset(value)
			}
		}
	}
	imageKey := "icon"
	if action(b.Action) == "status" || action(b.Action) == "track-info" {
		imageKey = "art"
		if status.Artwork != "" {
			base := status.Artwork
			if !strings.HasPrefix(base, "spotify:") {
				base = fmt.Sprintf("spotify:%x", sha256.Sum256([]byte(base)))
			}
			key := artworkVariant(base, artworkSize(b))
			encoded, _ := e.artCache.Get(key, 0)
			if encoded == "" && key != base {
				encoded, _ = e.artCache.Get(base, 0)
			}
			if artKey == key && artwork != "" {
				encoded = artwork
			}
			if encoded != "" {
				view.Image = encoded
				if field, ok := view.Feedback[imageKey].(map[string]any); ok {
					field["value"] = encoded
				} else {
					view.Feedback[imageKey] = encoded
				}
			}
		}
	}
	view.Image = engine.PortableImage(e.Engine, view.Image)
	err := e.SendDisplayView(ctx, c, id, view, action(b.Action) == "toggle")
	if err == nil && !e.spotifyObserved.IsZero() {
		if e.spotifyDelivery == nil {
			e.spotifyDelivery = map[string]time.Time{}
		}
		fallback := e.spotifyDefaultInterval
		if fallback <= 0 {
			fallback = time.Second
		}
		e.spotifyDelivery[id] = engine.RefreshNext(time.Now(), engine.RefreshInterval("spotify", action(b.Action), b.Settings, fallback))
	}
	return err
}

// Check whether any current binding requires Spotify observations
func (e *Plugin) spotifyDemand() bool {
	for _, b := range e.Bindings {
		if spotifyLive(action(b.Action)) {
			return true
		}
	}
	return false
}

// Read the observer status adjusted to the supplied time
func (e *Plugin) spotifyObservedStatus(now time.Time) spotify.SpotifyStatus {
	status := e.spotifySample
	limit := 3 * time.Second
	if e.spotifyObserver != nil && e.spotifyObserver.Healthy() {
		limit = 30 * time.Second
	}
	for _, b := range e.Bindings {
		interval := engine.RefreshInterval("spotify", action(b.Action), b.Settings, time.Second)
		if interval == 0 {
			limit = 1<<63 - 1
			break
		}
		limit = max(limit, interval*2)
	}
	if e.spotifyObserved.IsZero() || now.Sub(e.spotifyObserved) > limit {
		return spotify.SpotifyStatus{Mode: "unavailable"}
	}
	if status.Mode == "playing" && status.PositionSeconds != nil && status.Rate != nil && now.After(e.spotifyObserved) {
		position := math.Max(0, *status.PositionSeconds+now.Sub(e.spotifyObserved).Seconds()**status.Rate)
		status.PositionSeconds = &position
	}
	return status.Normalized()
}

// Schedule bounded Spotify work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Resolve or initialize the Spotify state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)

	return v
}

// Align Spotify observation with the current binding demand
func (e *Plugin) syncSpotifyObserver(now time.Time) {
	if e.Closed || !e.spotifyDemand() {
		if e.spotifyObserver != nil {
			e.spotifyObserver.Close()
			e.spotifyObserver = nil
		}
		e.CancelJob("spotify-status")
		e.spotifyObserverRetry = time.Time{}
		e.spotifyNext = time.Time{}
		e.spotifyObserved = time.Time{}
		e.spotifySample = spotify.SpotifyStatus{}
		e.spotifyFailures = 0
		e.spotifyArt = false
		e.spotifyRevision = 0
		e.artCache.Clear()
		return
	}
	if e.spotifyObserver != nil && e.spotifyObserver.Done() {
		e.spotifyObserver.Close()
		e.spotifyObserver = nil
		e.spotifyObserverRetry = now.Add(30 * time.Second)
		e.spotifyNext = maxTime(now, e.spotifyObserved.Add(250*time.Millisecond))
	}
	if e.spotifyObserver == nil && !now.Before(e.spotifyObserverRetry) {
		e.spotifyObserverRetry = now.Add(30 * time.Second)
		if e.observeSpotify != nil {
			e.spotifyObserver = e.observeSpotify(e.WakeChannel)
		}
		e.spotifyRevision = 0
	}
	if e.spotifyObserver != nil && e.spotifyObserver.Revision() != e.spotifyRevision {
		e.spotifyRevision = e.spotifyObserver.Revision()

		e.spotifyNext = maxTime(now, e.spotifyObserved.Add(250*time.Millisecond))
	}
}

// Compute the Spotify text shown for the selected action
func (e *Plugin) text(_ context.Context, _ string, _ binding) (string, error) {

	return "spotify", nil
}

// Format elapsed and total playback time labels
func spotifyTimingLabels(position, duration *float64) (string, string) {
	withHours := (duration != nil && *duration >= 3600) || (position != nil && *position >= 3600)
	label := func(value *float64) string {
		// PlaybackView normalizes nonfinite and negative readings to nil
		if value == nil || *value >= 1<<63 {
			return "--:--"
		}
		seconds := int64(*value)
		if withHours {
			return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
		}
		return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	return label(position), label(duration)
}

// PlaybackView builds the default display without polling or controlling Spotify
// Packaging review uses this same path as the live renderer
func PlaybackView(b engine.Binding, status spotify.SpotifyStatus) engine.Display {
	status = status.Normalized()
	a := action(b.Action)
	icon := "spotify/" + a + ".svg"
	if a == "toggle" {
		switch status.Mode {
		case "playing":
			icon = "spotify/pause.svg"
		case "paused", "stopped":
			icon = "spotify/play.svg"
		}
	}
	view := spotifyDisplay{Layout: "spotify/control.json", Image: icon, Feedback: map[string]any{"icon": icon}}
	if a == "toggle" && status.Mode == "playing" {
		view.State = 1
	}
	if a == "volume-display" || a == "progress" || a == "volume" || a == "seek" {
		value, indicator := "--%", 0.0
		title := "Volume"
		feedback := map[string]any{}
		valueKey := "value"
		if a == "volume-display" || a == "volume" {
			if status.VolumePercent != nil {
				indicator = *status.VolumePercent
				value = fmt.Sprintf("%.0f%%", indicator)
			}
			feedback["value"] = value
		} else {
			valueKey = "time"
			title = "Progress"
			position, duration := spotifyTimingLabels(status.PositionSeconds, status.DurationSeconds)
			value = position + " / " + duration
			if status.PositionSeconds != nil && status.DurationSeconds != nil && *status.DurationSeconds > 0 {
				indicator = *status.PositionSeconds / *status.DurationSeconds * 100
			}
			feedback["time"] = value
		}
		feedback["label"], feedback["indicator"] = title, indicator
		layout := "progress"
		if a == "volume" || a == "volume-display" {
			layout = "volume-display"
		}
		displayIcon := "spotify/" + layout + ".svg"
		feedback["icon"] = displayIcon
		if s := b.Surface; s != nil && s.Height > 0 && s.Width > s.Height*13/10 {
			size := s.Height * 60 / 100
			feedback["icon"] = map[string]any{"value": displayIcon, "rect": [4]int{4, 18, size * 200 / s.Width, 60}}
			feedback[valueKey] = map[string]any{"value": value, "rect": [4]int{69, 12, 123, 42}}
			feedback["label"] = map[string]any{"value": title, "rect": [4]int{69, 55, 123, 25}}
			feedback["indicator"] = map[string]any{"value": indicator, "rect": [4]int{69, 86, 123, 6}}
		}
		view.Layout, view.Title, view.Feedback = "spotify/"+layout+".json", title+"\n"+value, feedback
		return view
	}
	if a == "track-info" {
		title, artist := status.Title, status.Artist
		if title == "" {
			title = "No track information"
		}
		view.Layout, view.Title = "spotify/track-info.json", title+"\n"+artist
		view.Feedback = map[string]any{"art": "spotify/status.svg", "title": title, "artist": artist}
		return view
	}
	if a != "status" {
		width, height := 112, 112
		if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
			width, height = b.Surface.Width, b.Surface.Height
		}
		label := map[string]string{"toggle": "Play / Pause", "next": "Next", "previous": "Previous"}[a]
		if a == "toggle" && status.Mode == "playing" {
			label = "Pause"
		} else if a == "toggle" && (status.Mode == "paused" || status.Mode == "stopped") {
			label = "Play"
		}
		view.Title, view.Feedback["label"] = label, label
		size := min(width, height) * 62 / 100
		view.Feedback["icon"] = map[string]any{"value": icon, "rect": [4]int{(width - size) * 100 / width, 0, size * 200 / width, size * 100 / height}}
		if width > height*13/10 {
			size := height * 4 / 5
			view.Feedback["icon"] = map[string]any{"value": icon, "rect": [4]int{4, 10, size * 200 / width, 80}}
			view.Feedback["label"] = map[string]any{"value": label, "rect": [4]int{78, 10, 114, 80}}
		}
		return view
	}
	mode := map[string]string{"playing": "Playing", "paused": "Paused", "stopped": "Stopped", "buffering": "Buffering", "unavailable": "Unavailable", "unknown": "Unknown"}[status.Mode]
	position, duration := spotifyTimingLabels(status.PositionSeconds, status.DurationSeconds)
	timing := position + " / " + duration
	view.Layout = "spotify/status-key.json"
	if b.Surface != nil && b.Surface.Width > b.Surface.Height*13/10 {
		view.Layout = "spotify/status-wide.json"
	}
	view.Title = mode + "\n" + timing
	view.Feedback = map[string]any{"art": icon, "mode": mode, "time": timing, "position": "", "duration": ""}
	width, height := 112, 112
	if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
		width, height = b.Surface.Width, b.Surface.Height
	}

	scale := min(6, max(1, height/80))
	if len(timing)*8*scale > width {
		view.Feedback["time"] = ""
		view.Feedback["position"] = position
		view.Feedback["duration"] = "/ " + duration
	}
	return view
}

// ArtworkView keeps the same live cover source with no status or timing overlays
func ArtworkView(b engine.Binding) engine.Display {
	width, height := 112, 112
	if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
		width, height = b.Surface.Width, b.Surface.Height
	}
	size := min(width, height)
	return spotifyDisplay{Layout: "spotify/artwork.json", Image: "spotify/status.svg", Feedback: map[string]any{"art": map[string]any{"value": "spotify/status.svg", "rect": [4]int{(width - size) * 100 / width, (height - size) * 50 / height, size * 200 / width, size * 100 / height}}}}
}
