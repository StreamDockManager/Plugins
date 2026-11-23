package spotify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/cache"
	native "github.com/StreamDockManager/Plugins/internal/native/spotify"
)

// Verify artwork cache refresh eviction and snapshot
func TestArtworkCacheRefreshEvictionAndSnapshot(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	for i := range 16 {
		key := fmt.Sprint(i)
		if _, err := e.artCache.Put(key, key, len(key), 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := e.artworkSnapshot()
	// Rendering reads are intentionally not LRU touches
	if _, ok := e.artCache.Peek("0", 0); !ok {
		t.Fatal("missing cache hit")
	}
	e.artCache.Put("1", "refreshed", len("refreshed"), 0, 0)
	e.artCache.Put("16", "new", 3, 0, 0)
	if _, ok := e.artCache.Peek("0", 0); ok {
		t.Fatal("reads changed insertion eviction order")
	}
	if got, _ := e.artCache.Peek("1", 0); got != "refreshed" {
		t.Fatal("refresh lost", got)
	}
	if e.artCache.Len() != 16 {
		t.Fatal("entry cap", e.artCache.Len())
	}
	if snapshot["1"] != "1" || snapshot["0"] != "0" {
		t.Fatal("worker snapshot mutated", snapshot)
	}
	e.artCache.Clear()
	if e.artCache.Bytes() != 0 || e.artCache.Len() != 0 || len(snapshot) != 16 {
		t.Fatal("clear/snapshot ownership")
	}
}

// Verify artwork cache byte budget and invalid admission
func TestArtworkCacheByteBudgetAndInvalidAdmission(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	value := strings.Repeat("x", artworkCacheBytes/2)
	e.artCache.Put("first", value, len(value), 0, 0)
	e.artCache.Put("second", value, len(value), 0, 0)
	if e.artCache.Len() != 1 || e.artCache.Bytes() > artworkCacheBytes {
		t.Fatal("byte-cap eviction")
	}
	for _, entry := range []struct{ key, value string }{
		{strings.Repeat("k", 4097), "art"}, {"oversize", strings.Repeat("x", artworkCacheBytes)}, {"", "art"},
	} {
		before := e.artCache.Bytes()
		if _, err := e.artCache.Put(entry.key, entry.value, len(entry.value), 0, 0); err == nil {
			t.Fatal("invalid entry admitted")
		}
		if e.artCache.Bytes() != before {
			t.Fatal("failed admission altered retained entries")
		}
	}
}

// Verify artwork budget accommodates uncompressible render envelope
func TestArtworkBudgetAccommodatesUncompressibleRenderEnvelope(t *testing.T) {
	pixels := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	random := rand.New(rand.NewPCG(1, 2))
	for i := range pixels.Pix {
		pixels.Pix[i] = byte(random.Uint32())
	}
	var source bytes.Buffer
	if err := png.Encode(&source, pixels); err != nil {
		t.Fatal(err)
	}
	result, err := artwork(context.Background(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(source.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if 16*(len(result)+len("spotify:")+64+64) > artworkCacheBytes {
		t.Fatalf("render envelope exceeds budget: %d bytes", len(result))
	}
}

// Verify artwork display survives cache admission failure
func TestArtworkDisplaySurvivesCacheAdmissionFailure(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	e.artCache, _ = cache.New[string](16, 128)
	e.Bindings["key"] = binding{Action: "spotify.status"}
	source := "https://fixture.invalid/cover.png"
	artwork := "data:image/png;base64," + strings.Repeat("A", 256)
	key := fmt.Sprintf("spotify:%x", sha256.Sum256([]byte(source)))
	all := driveDisplays(t, e, []displayStep{{Render: func(ctx context.Context, c *sdk.Client) error {
		return e.applySpotify(ctx, c, jobResult{Value: spotifyProbe{Status: native.SpotifyStatus{Mode: "playing", Artwork: source}, ArtKey: key, Artwork: artwork}})
	}}})
	if e.spotifySample.Artwork != key {
		t.Fatal("playback snapshot retained the artwork source")
	}
	if e.artCache.Len() != 0 {
		t.Fatal("oversized entry cached")
	}
	found := false
	for _, event := range all[0] {
		found = found || strings.Contains(string(event.Payload), artwork)
	}
	if !found {
		t.Fatal("cache failure discarded valid display", all)
	}
}
