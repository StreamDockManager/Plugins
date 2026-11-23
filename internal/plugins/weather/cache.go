package weather

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	cache "github.com/StreamDockManager/Plugin-SDK/Go/cache"
	"net/url"
	"slices"
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
	e.cacheKeys = make(map[string]bool, len(keys.Keys))
	for _, key := range keys.Keys {
		e.cacheKeys[key] = true
	}
	if e.weather == nil {
		e.weather = map[string]weatherCache{}
	}
	e.cacheStore = store
	e.indexStore, _ = sdk.NewHostStore(c, "weather-forecast-index-v1")
	e.restoreForecastIndex(ctx)
}

// Only the endpoint selected by a binding needs a decoded forecast.
func (e *Plugin) restoreForecast(ctx context.Context, endpoint string) {
	key := forecastKey(endpoint)
	if e.cacheLRU != nil {
		if _, ok := e.cacheLRU.Get(endpoint, 0); ok && e.indexStore != nil {
			for i, entry := range e.indexOrder {
				if entry.Endpoint == endpoint {
					copy(e.indexOrder[i:], e.indexOrder[i+1:])
					e.indexOrder[len(e.indexOrder)-1] = entry
					break
				}
			}
		}
	}
	if _, ok := e.weather[endpoint]; ok || e.cacheStore == nil || !e.cacheKeys[key] {
		return
	}
	value, err := e.cacheStore.Get(ctx, key)
	if err != nil || !value.Found {
		return
	}
	var saved storedForecast
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		json.Unmarshal(value.Value, &saved) != nil || saved.Endpoint != endpoint || saved.Entry.At.After(time.Now().Add(time.Minute)) {
		return
	}
	saved.Entry.Used = time.Now()
	e.weather[endpoint] = saved.Entry
	if time.Now().Before(saved.Entry.RetryAt) {
		r := e.weatherRequests
		r.mu.Lock()
		if r.restored == nil {
			r.restored = map[string]*forecastHTTPError{}
		}
		r.restored[endpoint] = &forecastHTTPError{Status: 429, RetryAt: saved.Entry.RetryAt}
		r.mu.Unlock()
	}
}

type forecastIndexEntry struct {
	Endpoint string `json:"endpoint"`
	Cost     int    `json:"cost"`
}
type forecastIndex struct {
	Version sdk.StorageVersion   `json:"version"`
	Entries []forecastIndexEntry `json:"entries"`
}

func (e *Plugin) restoreForecastIndex(ctx context.Context) bool {
	if e.indexStore == nil {
		return false
	}
	record, err := e.indexStore.Get(ctx, "lru")
	if err != nil {
		e.indexStore = nil
		return false
	}
	e.indexVersion = record.StorageVersion
	var index forecastIndex
	if !record.Found || json.Unmarshal(record.Value, &index) != nil || index.Version != e.cacheVersion || len(index.Entries) != len(e.cacheKeys) {
		return false
	}
	lru, _ := cache.New[struct{}](forecastCacheEntries, forecastCacheBytes-len(forecastCacheNamespace))
	for _, entry := range index.Entries {
		if !e.cacheKeys[forecastKey(entry.Endpoint)] || entry.Cost < 1 {
			return false
		}
		evicted, err := lru.Put(entry.Endpoint, struct{}{}, entry.Cost, 0, 0)
		if err != nil || len(evicted) > 0 {
			return false
		}
	}
	if lru.Len() != len(e.cacheKeys) {
		return false
	}
	e.indexOrder = index.Entries
	e.cacheLRU = lru
	return true
}

func (e *Plugin) saveForecastIndex(ctx context.Context) {
	if e.indexStore == nil {
		return
	}
	index := forecastIndex{Version: e.cacheVersion, Entries: e.indexOrder}
	receipt, err := e.indexStore.Commit(ctx, e.indexVersion, sdk.OperationID(), []sdk.StorageMutation{{Op: "put", Key: "lru", Value: index}})
	if err != nil {
		e.indexStore = nil
		return
	}
	e.indexVersion = receipt.StorageVersion
}

// Older caches have no separate index. Read just their timestamps and lengths
// once on the first write, then maintain admission and eviction incrementally.
func (e *Plugin) initForecastLRU(ctx context.Context) bool {
	if e.cacheLRU != nil {
		return true
	}
	type metadata struct {
		key  string
		cost int
		used time.Time
	}
	entries := make([]metadata, 0, len(e.cacheKeys))
	for key := range e.cacheKeys {
		value, err := e.cacheStore.Get(ctx, key)
		if err != nil || value.StorageVersion != e.cacheVersion {
			return false
		}
		var saved struct {
			Endpoint string
			Entry    struct{ Used time.Time }
		}
		if !value.Found || json.Unmarshal(value.Value, &saved) != nil || forecastKey(saved.Endpoint) != key {
			continue
		}
		if active, ok := e.weather[saved.Endpoint]; ok && active.Used.After(saved.Entry.Used) {
			saved.Entry.Used = active.Used
		}
		entries = append(entries, metadata{saved.Endpoint, len(value.Value) + len(key), saved.Entry.Used})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].used.Equal(entries[j].used) {
			return entries[i].key < entries[j].key
		}
		return entries[i].used.Before(entries[j].used)
	})
	e.cacheLRU, _ = cache.New[struct{}](forecastCacheEntries, forecastCacheBytes-len(forecastCacheNamespace))
	for _, v := range entries {
		_, _ = e.cacheLRU.Put(v.key, struct{}{}, v.cost, 0, 0)
	}
	if e.indexStore != nil {
		ordered, _ := e.cacheLRU.Entries(0)
		for _, v := range ordered {
			e.indexOrder = append(e.indexOrder, forecastIndexEntry{v.Key, v.Cost})
		}
	}
	return true
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
	if !e.initForecastLRU(ctx) {
		// The storage generation changed or a read failed. Keep a bounded
		// session cache and leave the durable namespace to its current owner.
		e.cacheStore, e.cacheKeys, e.indexStore = nil, nil, nil
		e.initForecastLRU(ctx)
	}
	cost := len(encoded) + len(forecastKey(endpoint))
	evicted, err := e.cacheLRU.Put(endpoint, struct{}{}, cost, 0, 0)
	if err != nil {
		delete(e.weather, endpoint)
		return
	}
	if e.indexStore != nil {
		e.indexOrder = slices.DeleteFunc(e.indexOrder, func(entry forecastIndexEntry) bool {
			return entry.Endpoint == endpoint || slices.Contains(evicted, entry.Endpoint)
		})
		e.indexOrder = append(e.indexOrder, forecastIndexEntry{endpoint, cost})
	}
	var mutations []sdk.StorageMutation
	for _, old := range evicted {
		delete(e.weather, old)
		delete(e.cacheKeys, forecastKey(old))
		if e.weatherRequests != nil {
			e.weatherRequests.mu.Lock()
			delete(e.weatherRequests.restored, old)
			e.weatherRequests.mu.Unlock()
		}
		mutations = append(mutations, sdk.StorageMutation{Op: "delete", Key: forecastKey(old)})
	}
	mutations = append(mutations, sdk.StorageMutation{Op: "put", Key: forecastKey(endpoint), Value: json.RawMessage(encoded)})
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
		if mutation.Op == "put" {
			e.cacheKeys[mutation.Key] = true
		}
	}
	e.saveForecastIndex(ctx)
}
