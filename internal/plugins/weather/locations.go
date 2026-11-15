package weather

import (
	"context"
	"encoding/json"
	"math"
	"reflect"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

// A reference remains portable through its saved coordinate snapshot. Globals
// are resolved at read time; they never overwrite unrelated action preferences
func resolveLocation(settings, globals map[string]any) map[string]any {
	result := cloneSettings(settings)
	id, _ := result["location_id"].(string)
	if id == "" {
		return result
	}
	data, _ := json.Marshal(globals["saved_locations"])
	var locations []map[string]any
	if len(data) > 256*1024 || json.Unmarshal(data, &locations) != nil || len(locations) > 128 {
		return result
	}
	for _, location := range locations {
		lat, latOK := location["latitude"].(float64)
		lon, lonOK := location["longitude"].(float64)
		if location["id"] != id || !latOK || !lonOK || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			continue
		}
		result["latitude"], result["longitude"] = lat, lon
		result["location"] = cloneSettings(location)
		break
	}
	return result
}

// Process weather metadata and lifecycle updates before common dispatch
func (e *Plugin) beforeEvent(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	e.loadForecastCache(ctx, c)
	if ev.Event != "didReceiveGlobalSettings" {
		return false, nil
	}
	next := cloneSettings(ev.Data().Settings)
	previous := e.Global
	e.Global = next
	for id, binding := range e.Bindings {
		old, current := resolveLocation(binding.Settings, previous), resolveLocation(binding.Settings, next)
		if !reflect.DeepEqual(old, current) {
			delete(e.Effective, id)
			e.InvalidateProbe(id)
			delete(e.Last, id)
		}
	}
	return true, nil
}
