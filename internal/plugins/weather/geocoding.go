package weather

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go/httptrack"
)

const publicGeocodingEndpoint = "https://nominatim.openstreetmap.org/search"

// LocationSearch is transient inspector input, never binding settings
type LocationSearch struct {
	Mode    string `json:"mode"`
	Query   string `json:"query"`
	Country string `json:"country"`
}

type LocationResult struct {
	Label       string  `json:"label"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Attribution string  `json:"attribution"`
}

type geocodingEntry struct {
	At      time.Time        `json:"at"`
	Results []LocationResult `json:"results"`
}
type geocoder struct {
	mu       sync.Mutex
	cache    map[string]geocodingEntry
	client   *http.Client
	path     string
	loaded   bool
	next     time.Time
	cooldown time.Time
}

// Build a validated city or postal-code search URL
func geocodingURL(endpoint string, postalOnly bool, search LocationSearch) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = publicGeocodingEndpoint
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return "", fmt.Errorf("configure an HTTPS Nominatim search endpoint in Weather plugin settings")
	}
	query := strings.TrimSpace(search.Query)
	country := strings.ToLower(strings.TrimSpace(search.Country))
	if len(query) == 0 || len(query) > 512 || strings.ContainsAny(query, "\r\n\x00") {
		return "", fmt.Errorf("enter a ZIP/postal code or address (up to 512 characters)")
	}
	if country != "" && (len(country) != 2 || country[0] < 'a' || country[0] > 'z' || country[1] < 'a' || country[1] > 'z') {
		return "", fmt.Errorf("enter a two-letter country code")
	}
	q := url.Values{"format": {"jsonv2"}, "addressdetails": {"1"}, "limit": {"5"}}
	switch search.Mode {
	case "postal":
		if country == "" || len(query) > 20 {
			return "", fmt.Errorf("postal searches require a country and a postal code of up to 20 characters")
		}
		for _, c := range query {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == ' ' || c == '-') {
				return "", fmt.Errorf("check the postal code")
			}
		}
		q.Set("postalcode", query)
	case "address":
		if postalOnly {
			return "", fmt.Errorf("this service supports postal searches only; turn off Postal-only service or choose another endpoint")
		}
		q.Set("q", query)
	default:
		return "", fmt.Errorf("choose postal or address search")
	}
	if country != "" {
		q.Set("countrycodes", country)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Fetch and cache bounded geocoding results for the requested location
func (g *geocoder) search(ctx context.Context, endpoint string, postalOnly bool, search LocationSearch) ([]LocationResult, error) {
	requestURL, err := geocodingURL(endpoint, postalOnly, search)
	if err != nil {
		return nil, err
	}
	// Serialize and cache interactive requests. A shared broker remains necessary
	// to enforce an application-wide public-service budget across installations
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.load()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(requestURL)))
	if entry, ok := g.cache[key]; ok && time.Since(entry.At) < 24*time.Hour {
		return append([]LocationResult{}, entry.Results...), nil
	}
	u, _ := url.Parse(requestURL)
	public := strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "nominatim.openstreetmap.org")
	if public {
		if time.Now().Before(g.cooldown) {
			return nil, fmt.Errorf("location service asked us to wait; try again in %d seconds", max(1, int(time.Until(g.cooldown).Seconds())+1))
		}
		if wait := time.Until(g.next); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		g.next = time.Now().Add(1100 * time.Millisecond)
		g.save()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid geocoding request")
	}
	req.Header.Set("User-Agent", "stream-dock-manager-plugins/0.1.0 Weather (+https://github.com/StreamDockManager/Plugins)")
	req.Header.Set("Accept", "application/json")
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	// Prevent redirects from bypassing the configured endpoint/privacy boundary
	owned := *client
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httptrack.TrackHTTPClient(&owned).Do(req)
	if err != nil {
		return nil, fmt.Errorf("location search failed; check the service connection or try again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if public && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable) {
			wait := time.Minute
			if seconds, err := time.ParseDuration(response.Header.Get("Retry-After") + "s"); err == nil && seconds > 0 {
				wait = seconds
			} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(at) > 0 {
				wait = time.Until(at)
			}
			g.cooldown = time.Now().Add(wait)
			g.save()
		}
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("location service is busy (HTTP 429); wait before searching again. Retry-After: %.80s", response.Header.Get("Retry-After"))
		}
		return nil, fmt.Errorf("location service HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return nil, fmt.Errorf("location response is too large or unreadable")
	}
	var raw []struct {
		Name string `json:"display_name"`
		Lat  string `json:"lat"`
		Lon  string `json:"lon"`
	}
	if json.Unmarshal(data, &raw) != nil || raw == nil || len(raw) > 40 {
		return nil, fmt.Errorf("invalid location response")
	}
	results := make([]LocationResult, 0, min(5, len(raw)))
	for _, item := range raw {
		lat, latErr := strconv.ParseFloat(item.Lat, 64)
		lon, lonErr := strconv.ParseFloat(item.Lon, 64)
		if latErr != nil || lonErr != nil || math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || lat < -90 || lat > 90 || lon < -180 || lon > 180 || strings.TrimSpace(item.Name) == "" || len(item.Name) > 2048 {
			return nil, fmt.Errorf("invalid coordinates or label in location response")
		}
		results = append(results, LocationResult{item.Name, lat, lon, "© OpenStreetMap contributors (ODbL)"})
		if len(results) == 5 {
			break
		}
	}
	if g.cache == nil {
		g.cache = map[string]geocodingEntry{}
	}
	if len(g.cache) >= 64 {
		clear(g.cache)
	}
	g.cache[key] = geocodingEntry{time.Now(), results}
	g.save()
	return append([]LocationResult{}, results...), nil
}

// Cache hashes avoid storing raw search strings. The file is local to Weather,
// private, bounded, atomic, and independent of binding/plugin settings drafts
func (g *geocoder) load() {
	if g.loaded {
		return
	}
	g.loaded = true
	if g.path == "" {
		return
	}
	f, err := os.Open(g.path)
	if err != nil {
		return
	}
	defer f.Close()
	var disk struct {
		Entries  map[string]geocodingEntry `json:"entries"`
		Next     time.Time                 `json:"next"`
		Cooldown time.Time                 `json:"cooldown"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&disk) != nil || len(disk.Entries) > 64 {
		return
	}
	g.cache = map[string]geocodingEntry{}
	for key, entry := range disk.Entries {
		if len(key) != 64 || time.Since(entry.At) < 0 || time.Since(entry.At) >= 24*time.Hour || len(entry.Results) > 5 {
			continue
		}
		valid := true
		for _, r := range entry.Results {
			if r.Label == "" || len(r.Label) > 2048 || math.IsNaN(r.Latitude) || math.IsNaN(r.Longitude) || math.IsInf(r.Latitude, 0) || math.IsInf(r.Longitude, 0) || math.Abs(r.Latitude) > 90 || math.Abs(r.Longitude) > 180 {
				valid = false
				break
			}
		}
		if valid {
			g.cache[key] = entry
		}
	}
	if time.Until(disk.Next) < 2*time.Second {
		g.next = disk.Next
	}
	if time.Until(disk.Cooldown) < 24*time.Hour {
		g.cooldown = disk.Cooldown
	}
}

// Store the current bounded geocoding cache snapshot
func (g *geocoder) save() {
	if g.path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(g.path), 0700) != nil {
		return
	}
	data, err := json.Marshal(struct {
		Entries  map[string]geocodingEntry `json:"entries"`
		Next     time.Time                 `json:"next"`
		Cooldown time.Time                 `json:"cooldown"`
	}{g.cache, g.next, g.cooldown})
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(g.path), ".weather-location-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return
	}
	if f.Close() == nil {
		_ = os.Rename(f.Name(), g.path)
	}
}
