package weather

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	cache "github.com/StreamDockManager/Plugin-SDK/Go/cache"
	"net/url"
	"sort"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

const forecastCacheBytes = 8 * 1024 * 1024
const forecastCacheEntries = 128
const forecastCacheNamespace = "weather-forecast-v1"

type storedForecast struct {
	Endpoint string       `json:"endpoint"`
	Entry    weatherCache `json:"entry"`
}

// Derive the stable private-storage key for a forecast endpoint
func forecastKey(endpoint string) string {
	return fmt.Sprintf("forecast-%x", sha256.Sum256([]byte(endpoint)))
}

// Restore validated forecast entries from negotiated host storage
func (e *Plugin) loadForecastCache(ctx context.Context, c *sdk.Client) {
	if e.cacheLoaded || c == nil {
		return
	}
	e.cacheLoaded = true
	store, err := sdk.NewHostStore(c, forecastCacheNamespace)
	if err != nil {
		return
	}
	keys, err := store.ListKeys(ctx, "")
	if err != nil || keys.Cursor != "" || len(keys.Keys) > forecastCacheEntries {
		return
	}
	e.cacheVersion = keys.StorageVersion
	if e.weather == nil {
		e.weather = map[string]weatherCache{}
	}
	total := len(forecastCacheNamespace)
	for _, key := range keys.Keys {
		value, err := store.Get(ctx, key)
		if err != nil {
			return
		}
		total += len(value.Value) + len(key)
		if total > forecastCacheBytes {
			return
		}
		var saved storedForecast
		if json.Unmarshal(value.Value, &saved) != nil || forecastKey(saved.Endpoint) != key {
			continue
		}
		parsed, err := url.Parse(saved.Endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			continue
		}
		if saved.Entry.At.After(time.Now().Add(time.Minute)) {
			continue
		}
		e.weather[saved.Endpoint] = saved.Entry
		e.cacheVersion = value.StorageVersion
		if time.Now().Before(saved.Entry.RetryAt) {
			if e.weatherRequests.restored == nil {
				e.weatherRequests.restored = map[string]*forecastHTTPError{}
			}
			e.weatherRequests.restored[saved.Endpoint] = &forecastHTTPError{Status: 429, RetryAt: saved.Entry.RetryAt}
		}
	}
	e.cacheStore = store
}

// Store each response independently to respect the host's 256 KiB value limit
// A failed/indeterminate write disables further writes for this runtime; a new
// session reloads authoritative state instead of guessing the commit outcome
func (e *Plugin) persistForecast(ctx context.Context, endpoint string) {
	entry := e.weather[endpoint]
	entry.Used = time.Now()
	e.weather[endpoint] = entry
	encoded, err := json.Marshal(storedForecast{endpoint, entry})
	if err != nil || len(encoded) > sdk.MaximumStorageValue {
		delete(e.weather, endpoint)
		return
	}
	// Preserve the existing on-host forecast format and timestamp ordering
	// the SDK owns entry/byte admission and eviction
	bounded, _ := cache.New[weatherCache](forecastCacheEntries, forecastCacheBytes-len(forecastCacheNamespace))
	keys := make([]string, 0, len(e.weather))
	for key := range e.weather {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := e.weather[keys[i]].Used, e.weather[keys[j]].Used
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.Before(b)
	})
	var mutations []sdk.StorageMutation
	for _, key := range keys {
		data, _ := json.Marshal(storedForecast{key, e.weather[key]})
		_, _ = bounded.Put(key, e.weather[key], len(data)+len(forecastKey(key)), 0, 0)
	}
	for _, key := range keys {
		if _, ok := bounded.Peek(key, 0); !ok {
			delete(e.weather, key)
			mutations = append(mutations, sdk.StorageMutation{Op: "delete", Key: forecastKey(key)})
		}
	}
	if _, exists := e.weather[endpoint]; exists {
		mutations = append(mutations, sdk.StorageMutation{Op: "put", Key: forecastKey(endpoint), Value: storedForecast{endpoint, entry}})
	}
	if e.cacheStore == nil {
		return
	}
	// Usually one put and one eviction; batch conservatively for bounded RPCs
	for _, mutation := range mutations {
		receipt, err := e.cacheStore.Commit(ctx, e.cacheVersion, sdk.OperationID(), []sdk.StorageMutation{mutation})
		if err != nil {
			e.cacheStore = nil
			return
		}
		e.cacheVersion = receipt.StorageVersion
	}
}
