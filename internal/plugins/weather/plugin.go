package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cache "github.com/StreamDockManager/Plugin-SDK/Go/cache"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type Plugin struct {
	vaultClient  *sdk.Client
	cacheLRU     *cache.Cache[struct{}]
	cacheKeys    map[string]bool
	cacheLoaded  bool
	indexStore   *sdk.HostStore
	indexOrder   []forecastIndexEntry
	indexVersion sdk.StorageVersion
	cacheStore   *sdk.HostStore
	cacheVersion sdk.StorageVersion
	*engine.Engine
	telemetryExpiry  time.Time
	readStatus       func(context.Context, statusRequest) probeStatus
	weather          map[string]weatherCache
	weatherClient    *http.Client
	weatherRequests  *weatherRequests
	lastWeatherError error
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type probeStatus struct {
	Status  displayStatus
	Weather map[string]weatherCache
	RetryAt time.Time
}

type sampledStatus = engine.SampledStatus

type statusRequest struct {
	vaultClient     *sdk.Client
	binding         binding
	weather         map[string]weatherCache
	weatherClient   *http.Client
	weatherRequests *weatherRequests
}

type weatherCache struct {
	At      time.Time
	Data    map[string]any
	Error   string
	Used    time.Time
	RetryAt time.Time
	Refresh bool `json:"-"`
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var cloneSettings = engine.CloneSettings

// Attach weather lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize weather state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, weatherRequests: &weatherRequests{}}
	return e
}

// Apply completed weather work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	if strings.HasPrefix(result.ID, "status:") {
		return e.applyStatus(ctx, c, result, job.Refresh)
	}
	return e.FinishJob(ctx, c, result, job)
}

// Update shared weather samples and refresh their bound displays
func (e *Plugin) applyStatus(ctx context.Context, c *sdk.Client, result jobResult, refresh bool) error {
	value, ok := result.Value.(probeStatus)
	if !ok {
		return nil
	}
	now := time.Now()
	// Update current sample owners while retaining refresh and failure-backoff policy
	return engine.CompleteSample(e.Engine, result.ID, func(id string, b binding, previous sampledStatus) sampledStatus {
		configured := engine.RefreshInterval("weather", action(b.Action), b.Settings, time.Hour)
		interval := configured
		failures := 0
		if value.Status.Unavailable {
			failures = min(previous.Failures+1, 6)
			interval = max(configured, min(time.Duration(1<<failures)*time.Second, 60*time.Second))
		}
		if refresh && !value.Status.Unavailable {
			interval = 0
		}
		next := weatherNextAttempt(now, interval, value.RetryAt)
		if !value.Status.Unavailable && !refresh && configured > 0 {
			endpoint, _ := weatherURL(resolveLocation(e.MergedSettings(b.Settings), e.Global))
			if cached, ok := value.Weather[endpoint]; ok && !cached.At.IsZero() {
				next = weatherNextAttempt(cached.At, configured, value.RetryAt)
			}
		}
		if configured == 0 && !refresh {
			next = engine.RefreshNext(now, 0)
		}
		return sampledStatus{Status: value.Status, At: now, Failures: failures, Next: next}
	}, func(id string, status displayStatus) error { return e.SendDisplay(ctx, c, id, status) }, func() {
		if value.Weather != nil {
			if e.weather == nil {
				e.weather = map[string]weatherCache{}
			}
			for key, entry := range value.Weather {
				previous, exists := e.weather[key]
				if e.weatherInUse(key, "") && (!exists || entry.At.After(previous.At) || (entry.At.Equal(previous.At) && entry.RetryAt.After(previous.RetryAt))) {
					e.weather[key] = entry
					e.persistForecast(ctx, key)
				}
			}
		}
	})
}

// Clear weather transient resources during feature shutdown
func (e *Plugin) closeFeature() {
	clear(e.weather)
	if e.weatherRequests != nil {
		e.weatherRequests.close()
	}
}

// Reuse a current weather sample or schedule a bounded refresh
func (e *Plugin) collectStatus(ctx context.Context, id string, b binding) displayStatus {
	previous := e.Statuses[id]
	key := e.ProbeKey(id, b)
	if previous.At.IsZero() && !enabledVault(b.Settings) {
		resolved := resolveLocation(e.MergedSettings(b.Settings), e.Global)
		endpoint, _ := weatherURL(resolved)
		e.restoreForecast(ctx, endpoint)
		if cached, ok := e.weather[endpoint]; ok && !cached.At.IsZero() {
			if text, err := weatherText(cached.Data, action(b.Action), configvalue.String(resolved, "units", "celsius")); err == nil {
				interval := engine.RefreshInterval("weather", action(b.Action), b.Settings, time.Hour)
				previous = sampledStatus{Status: displayStatus{Text: text}, At: cached.At, Next: engine.RefreshNext(cached.At, interval)}
				if cached.Refresh {
					previous.Next = time.Time{}
				}
				e.Statuses[id] = previous
				e.persistForecast(ctx, endpoint)
			}
		}
	}
	// Snapshot binding settings before handing work to a background probe
	return engine.CollectSample(e.Engine, id, key, previous, time.Now, func() bool {

		request := statusRequest{vaultClient: e.vaultClient, binding: b, weather: e.weather, weatherClient: e.weatherClient, weatherRequests: e.weatherRequests}
		request.binding.Settings = resolveLocation(e.MergedSettings(b.Settings), e.Global)
		{

			request.weatherClient = e.weatherClient
			endpoint, _ := weatherURL(request.binding.Settings)
			request.weather = map[string]weatherCache{}
			if cached, ok := e.weather[endpoint]; ok {
				request.weather[endpoint] = cached
			}
		}

		read := e.readStatus
		if read == nil {
			read = func(ctx context.Context, request statusRequest) probeStatus { return request.sample(ctx) }
		}
		return e.startJob(ctx, key, func(ctx context.Context) (string, any, error) {
			value := read(ctx, request)
			return "", value, nil
		})
	}, engine.StaleSample)
}

// Build the current weather status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {
	if enabledVault(b.Settings) {
		return displayStatus{Text: "Waiting for shared location", Unavailable: true}
	}

	text, err := e.text(ctx, id, b)
	if err != nil {
		return displayStatus{Text: err.Error(), Unavailable: true}
	}
	if text == "weather" {
		a := action(b.Action)
		if a != "" {
			text = strings.ToUpper(a[:1]) + a[1:]
		}
	}
	state := 0

	return displayStatus{Text: text, State: state, Unavailable: e.lastWeatherError != nil}
}

// Route weather inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op        string         `json:"op"`
		RequestID string         `json:"request_id"`
		Settings  map[string]any `json:"settings"`
		Scope     string         `json:"scope"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Op == "search_location" || p.Op == "cancel_location_search" {
		return true, fmt.Errorf("Use Find a location in Manager")
	}
	if p.Op != "discover" && p.Op != "test" {
		return false, nil
	}
	s := resolveLocation(e.MergedSettings(p.Settings), e.Global)
	a := action(ev.Action)
	if a == "" {
		a = "refresh"
	}
	if p.Op == "discover" {

		return true, engine.SendDiagnostic(ctx, c, ev.Context, ev.Action, map[string]any{"message": "This action has no runtime discovery"})

	}
	{

		if enabledVault(s) {
			request := statusRequest{vaultClient: c, weatherClient: e.weatherClient, binding: binding{Action: ev.Action, Settings: s}}
			e.startJob(ctx, ev.Context, func(job context.Context) (string, any, error) {
				result := request.sampleLocation(job)
				return result.Status.Text, map[string]any{"message": result.Status.Text, "available": !result.Status.Unavailable}, nil
			})
			return true, nil
		}
		endpoint, _ := weatherURL(s)
		e.restoreForecast(ctx, endpoint)
		sample := Plugin{weatherRequests: e.weatherRequests, weatherClient: e.weatherClient, weather: map[string]weatherCache{}}
		if cached, ok := e.weather[endpoint]; ok {
			sample.weather[endpoint] = cached
		}
		e.startJob(ctx, ev.Context, func(job context.Context) (string, any, error) {
			text, err := sample.weatherText(job, a, s)
			message := text
			if err != nil {
				message = err.Error()
			}
			endpoint, _ := weatherURL(s)
			cached := sample.weather[endpoint]
			stale := !cached.At.IsZero() && time.Since(cached.At) > engine.RefreshInterval("weather", a, s, time.Hour)
			current, _ := cached.Data["current"].(map[string]any)
			return text, map[string]any{"message": message, "available": err == nil, "attribution": "Open-Meteo (CC BY 4.0)", "cached": stale, "stale": stale, "error": cached.Error, "weather_code": current["weather_code"]}, err
		})
		return true, nil
	}

}

// Dispatch an activating weather input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	s := b.Settings
	if !active(ev) && ev.Event != "dialRotate" {
		return nil
	}

	if ev.Event == "dialRotate" {
		delete(e.Effective, ev.Context)

		return nil

	}
	if !active(ev) {
		return nil
	}
	{

		if enabledVault(s) {
			e.RefreshProbe(ev.Context)
		} else if endpoint, err := weatherURL(resolveLocation(e.MergedSettings(s), e.Global)); err == nil {
			if cached, exists := e.weather[endpoint]; exists {
				cached.Refresh = true
				e.weather[endpoint] = cached
			}
		}
	}

	return e.Render(ctx, c, ev.Context)
}

// Expose the weather feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Before: e.beforeEvent, Action: e.handleAction, Settings: e.settingsFeature, Inspector: e.extensionInspector, Close: e.closeFeature, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals, View: e.displayView, Collect: e.collectStatus}
}

// Choose the next explicit weather deadline without idle polling
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
	for id, b := range e.Bindings {
		{
			{

				key := e.ProbeKey(id, b)
				if _, busy := e.Jobs[key]; !busy {
					if status, ok := e.Statuses[id]; ok {
						earlier(status.Next)
					} else {
						earlier(now)
					}
				}
			}
		}

	}

	earlier(e.PendingWrite)
	return deadline
}

// Read a detached weather status request away from binding mutation
func (r statusRequest) sample(ctx context.Context) probeStatus {
	if enabledVault(r.binding.Settings) {
		return r.sampleLocation(ctx)
	}
	sample := Plugin{weather: r.weather, weatherClient: r.weatherClient, weatherRequests: r.weatherRequests}
	status := sample.displayStatus(ctx, "", r.binding)
	var retry time.Time
	if failure, ok := errors.AsType[*forecastHTTPError](sample.lastWeatherError); ok {
		retry = failure.RetryAt
	}
	return probeStatus{Status: status, Weather: sample.weather, RetryAt: retry}
}

// Schedule bounded weather work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Compute the weather text shown for the selected action
func (e *Plugin) text(ctx context.Context, _ string, b binding) (string, error) {
	s := b.Settings
	a := action(b.Action)

	return e.weatherText(ctx, a, s)

}

// Check whether another binding still uses the forecast endpoint
func (e *Plugin) weatherInUse(endpoint, except string) bool {
	for id, binding := range e.Bindings {
		if id == except {
			continue
		}
		if other, err := weatherURL(resolveLocation(e.MergedSettings(binding.Settings), e.Global)); err == nil && other == endpoint {
			return true
		}
	}
	return false
}

// Fetch or reuse the configured forecast and format the requested measurement
func (e *Plugin) weatherText(ctx context.Context, action string, settings map[string]any) (string, error) {
	e.lastWeatherError = nil
	if err := ctx.Err(); err != nil {
		return "", err
	}
	endpoint, err := weatherURL(settings)
	if err != nil {
		return "", err
	}
	e.restoreForecast(ctx, endpoint)
	cached, ok := e.weather[endpoint]
	units := configvalue.String(settings, "units", "celsius")
	interval := engine.RefreshInterval("weather", action, settings, time.Hour)
	if !ok || cached.At.IsZero() || cached.Refresh || (interval > 0 && time.Since(cached.At) >= interval) {
		var data map[string]any
		if e.weatherRequests != nil {
			data, err = e.weatherRequests.get(ctx, endpoint, e.weatherClient)
		} else {
			requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			data, err = fetchWeather(requestCtx, endpoint, e.weatherClient)
			cancel()
		}
		if err == nil {
			_, err = weatherText(data, action, units)
		}
		if err == nil {
			cached = weatherCache{At: time.Now(), Data: data}
			if e.weather == nil {
				e.weather = map[string]weatherCache{}
			}
			e.weather[endpoint] = cached
		}

		if err != nil {
			e.lastWeatherError = err
			cached.Refresh = false
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if failure, ok := errors.AsType[*forecastHTTPError](err); ok {
				cached.RetryAt = failure.RetryAt
				if e.weather == nil {
					e.weather = map[string]weatherCache{}
				}
				e.weather[endpoint] = cached
			}
			if !ok || len(cached.Data) == 0 {
				return "", err
			}
			text, formatErr := weatherText(cached.Data, action, units)
			if formatErr != nil {
				return "", err
			}
			cached.Error = err.Error()
			e.weather[endpoint] = cached
			return text, nil
		}
	}
	return weatherText(cached.Data, action, units)
}

// Format the selected weather measurement and units
func weatherText(data map[string]any, action, units string) (string, error) {
	unit := "°C"
	if units == "fahrenheit" {
		unit = "°F"
	}
	current, _ := data["current"].(map[string]any)
	daily, _ := data["daily"].(map[string]any)
	first := func(key string) any {
		values, _ := daily[key].([]any)
		if len(values) > 0 {
			return values[0]
		}
		return nil
	}
	text := ""
	switch action {
	case "forecast":
		high, okHigh := first("temperature_2m_max").(float64)
		low, okLow := first("temperature_2m_min").(float64)
		if !okHigh || !okLow {
			return "", fmt.Errorf("weather response is missing daily temperatures")
		}
		text = fmt.Sprintf("%.1f %s\n%.1f %s", low, unit, high, unit)
	case "sun":
		rise, okRise := first("sunrise").(string)
		set, okSet := first("sunset").(string)
		clock := func(value string) string {
			if _, clock, found := strings.Cut(value, "T"); found {
				return clock
			}
			return value
		}
		if !okRise || !okSet || rise == "" || set == "" {
			return "", fmt.Errorf("weather response is missing sunrise/sunset")
		}
		text = clock(rise) + "\n" + clock(set)
	default:
		temperature, okTemp := current["temperature_2m"].(float64)
		code, okCode := current["weather_code"].(float64)
		if !okTemp || !okCode {
			return "", fmt.Errorf("weather response is missing current conditions")
		}
		_ = code
		text = fmt.Sprintf("%.1f %s", temperature, unit)
	}
	return text, nil
}

// Build a forecast URL from validated location settings
func weatherURL(settings map[string]any) (string, error) {
	if enabledVault(settings) {
		return "", fmt.Errorf("Choose a location in Manager")
	}
	endpoint := configvalue.String(settings, "endpoint", "https://api.open-meteo.com/v1/forecast")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", fmt.Errorf("weather endpoint must use HTTPS")
	}
	// Default only an absent setting so explicit false and malformed values remain rejected
	_, noncommercialSet := settings["noncommercial"]
	if strings.EqualFold(u.Hostname(), "api.open-meteo.com") && noncommercialSet && !configvalue.Bool(settings, "noncommercial") {
		return "", fmt.Errorf("confirm noncommercial free-service use in inspector or configure paid/self-hosted endpoint")
	}
	lat, lon := configvalue.Number(settings, "latitude", 0), configvalue.Number(settings, "longitude", 0)
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return "", fmt.Errorf("invalid latitude/longitude")
	}
	units := configvalue.String(settings, "units", "celsius")
	if units != "celsius" && units != "fahrenheit" {
		return "", fmt.Errorf("choose Celsius or Fahrenheit")
	}
	q := u.Query()
	q.Set("latitude", fmt.Sprint(lat))
	q.Set("longitude", fmt.Sprint(lon))
	q.Set("current", "temperature_2m,weather_code")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,sunrise,sunset")
	q.Set("timezone", "auto")
	q.Set("temperature_unit", units)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Honor both refresh cadence and the provider retry deadline
func weatherNextAttempt(now time.Time, interval time.Duration, retry time.Time) time.Time {
	next := now.Add(interval)
	if retry.After(next) {
		next = retry
	}
	return next
}

// The authoring lifecycle cancels only the changed binding's probe. Shared
// requests retain their other consumers and unrelated locations keep running
func (e *Plugin) settingsFeature(string) {}
