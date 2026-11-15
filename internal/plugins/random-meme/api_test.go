package randommeme

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

// Delegate the fixture HTTP request to its injected response handler
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Encode an image fixture for meme response tests
func testImage(t *testing.T) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 640, 240))
	for y := range 240 {
		for x := range 640 {
			pixels.Set(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, pixels); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Create a meme API client using an isolated test server
func fixtureAPI(t *testing.T, memes []Meme) (*API, *int, *int) {
	t.Helper()
	metadata, images := 0, 0
	data := testImage(t)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch r.URL.Host {
		case "meme-api.com":
			meme := memes[min(metadata, len(memes)-1)]
			metadata++
			body, _ = json.Marshal(meme)
		case "i.redd.it":
			images++
			body = data
		default:
			t.Errorf("unexpected network destination %s", r.URL.Host)
		}
		if r.Method != "GET" {
			t.Errorf("unexpected write request %s", r.Method)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	return &API{Client: client, Endpoint: providerURL}, &metadata, &images
}

// Verify fetch filters NSFW by default and supports explicit opt in
func TestFetchFiltersNSFWByDefaultAndSupportsExplicitOptIn(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "enabled"}[allow], func(t *testing.T) {
			api, calls, images := fixtureAPI(t, []Meme{{URL: "https://i.redd.it/adult.png", NSFW: true}, {URL: "https://i.redd.it/safe.png"}})
			meme, err := api.Fetch(context.Background(), "memes", allow)
			if err != nil {
				t.Fatal(err)
			}
			if meme.NSFW != allow || *images != 1 {
				t.Fatal("content filter failed", meme.NSFW, *images)
			}
			want := 2
			if allow {
				want = 1
			}
			if *calls != want {
				t.Fatal("wrong attempts", *calls)
			}
			if meme.Width != 320 || meme.Height != 120 {
				t.Fatal("thumbnail aspect changed", meme.Width, meme.Height)
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(meme.Image, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() != 320 {
				t.Fatal("invalid raster preview", err)
			}
		})
	}
}

// Verify blocked content never downloads and attempts are bounded
func TestBlockedContentNeverDownloadsAndAttemptsAreBounded(t *testing.T) {
	for _, meme := range []Meme{{URL: "https://i.redd.it/adult.png", NSFW: true}, {URL: "https://i.redd.it/spoiler.png", Spoiler: true}, {URL: "http://127.0.0.1/private"}, {URL: "https://example.com/image.png"}} {
		api, calls, images := fixtureAPI(t, []Meme{meme})
		if _, err := api.Fetch(context.Background(), "", false); err == nil || *calls != 3 || *images != 0 {
			t.Fatal("unsafe or unbounded fetch", err, *calls, *images)
		}
	}
}

// Verify fetch rejects malformed metadata and subreddits
func TestFetchRejectsMalformedMetadataAndSubreddits(t *testing.T) {
	api, calls, _ := fixtureAPI(t, []Meme{{}})
	if _, err := api.Fetch(context.Background(), "../private", false); err == nil || *calls != 0 {
		t.Fatal("invalid subreddit accepted")
	}
	if _, err := api.Fetch(context.Background(), "", false); err == nil {
		t.Fatal("missing image accepted")
	}
	for _, value := range []string{"https://user:password@i.redd.it/a", "https://i.redd.it:444/a", "https://i.redd.it/a\nenter", "file:///etc/passwd"} {
		if imageURL(value) == nil {
			t.Fatal("invalid link accepted", value)
		}
	}
}

// Verify thumbnail rejects huge or corrupt images
func TestThumbnailRejectsHugeOrCorruptImages(t *testing.T) {
	var huge bytes.Buffer
	if err := png.Encode(&huge, image.NewGray(image.Rect(0, 0, 8193, 1))); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("not an image"), huge.Bytes()} {
		if _, _, _, err := thumbnailSizeContext(context.Background(), data, 0, 0, false); err == nil {
			t.Fatal("unbounded image accepted")
		}
	}
}
