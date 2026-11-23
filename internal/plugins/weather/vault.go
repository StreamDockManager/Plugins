package weather

import (
	"context"
	"errors"
	"net/http"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
)

// Resolve a grant for each refresh and keep the coordinate-bearing URL local to this request.
func (r statusRequest) sampleLocation(ctx context.Context) probeStatus {
	failed := func(text string) probeStatus {
		return probeStatus{Status: displayStatus{Text: text, Unavailable: true}}
	}
	if r.vaultClient == nil {
		return failed("Review location access in Manager")
	}
	location, err := r.vaultClient.ReadVaultLocation(ctx, sdk.VaultValueRequest{Action: r.binding.Action, Slot: "location", Field: "location"})
	if err != nil {
		return failed("Review location access in Manager")
	}
	settings := cloneSettings(r.binding.Settings)
	delete(settings, "use_vault_location")
	settings["latitude"], settings["longitude"] = location.Latitude, location.Longitude
	endpoint, err := weatherURL(settings)
	if err != nil {
		return failed(err.Error())
	}
	client := http.Client{Timeout: 8 * time.Second}
	if r.weatherClient != nil {
		client = *r.weatherClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	data, err := readForecast(requestCtx, endpoint, &client)
	if err != nil {
		status := failed("Weather service unavailable")
		if failure, ok := errors.AsType[*forecastHTTPError](err); ok {
			status.RetryAt = failure.RetryAt
		}
		return status
	}
	text, err := weatherText(data, action(r.binding.Action), configvalue.String(settings, "units", "celsius"))
	if err != nil {
		return failed(err.Error())
	}
	return probeStatus{Status: displayStatus{Text: text}}
}
