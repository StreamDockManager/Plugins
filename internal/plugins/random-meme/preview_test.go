package randommeme

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Encode deterministic PNG pixels with optional transparency
func previewFixture(t *testing.T, w, h int, transparent bool) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if transparent && x < w/2 {
				a = 0
			}
			pixels.SetNRGBA(x, y, color.NRGBA{uint8(x*17 + y*31), uint8(x*37 + y*13), uint8(x*5 + y*71), a})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, pixels); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Encode a JPEG preview fixture with known dimensions
func jpegPreviewFixture(t *testing.T) []byte {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(previewFixture(t, 800, 400, false)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, decoded, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type countedBody struct {
	*bytes.Reader
	read   int
	closed bool
}

// Count fixture response bytes as they are read
func (b *countedBody) Read(p []byte) (int, error) { n, e := b.Reader.Read(p); b.read += n; return n, e }

// Record closure of the fixture-owned resource
func (b *countedBody) Close() error { b.closed = true; return nil }

// Verify preview preserves original link aspect and pixels
func TestPreviewPreservesOriginalLinkAspectAndPixels(t *testing.T) {
	original := jpegPreviewFixture(t)
	expected, w, h, err := thumbnailSizeContext(context.Background(), original, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(expected, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	source := &countedBody{Reader: bytes.NewReader(original)}
	candidate := &countedBody{Reader: bytes.NewReader(preview)}
	requests := []string{}
	metadata, _ := json.Marshal(map[string]any{"url": "https://i.redd.it/source.png", "postLink": "https://redd.it/fixture", "preview": []string{"https://preview.redd.it/too-small.png?width=216", "https://preview.redd.it/large.png?width=640", "https://preview.redd.it/selected.png?width=320"}})
	api := &API{Endpoint: providerURL, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		var body io.ReadCloser = io.NopCloser(bytes.NewReader(metadata))
		switch r.URL.Host {
		case "i.redd.it":
			body = source
		case "preview.redd.it":
			if r.URL.Path != "/selected.png" {
				t.Fatalf("wrong preview %s", r.URL)
			}
			body = candidate
		}
		return &http.Response{StatusCode: 200, Body: body}, nil
	})}}
	meme, err := api.Fetch(context.Background(), "", false)
	if err != nil || meme.URL != "https://i.redd.it/source.png" || meme.PostLink != "https://redd.it/fixture" || meme.Image != expected || meme.Width != w || meme.Height != h || w != 320 || h != 160 {
		t.Fatal("preview changed output", meme, err)
	}
	if len(requests) != 3 || source.read > 64<<10 || !source.closed || !candidate.closed {
		t.Fatal("preview request/ownership bound", requests, source.read)
	}

}

// Verify preview failure falls back without refetch or output change
func TestPreviewFailureFallsBackWithoutRefetchOrOutputChange(t *testing.T) {
	original := jpegPreviewFixture(t)
	expected, w, h, err := thumbnailSizeContext(context.Background(), original, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"invalid", []byte("bad image"), 200}, {"too small", previewFixture(t, 160, 80, true), 200},
		{"changed opacity", previewFixture(t, 320, 160, true), 200},
		{"cropped", previewFixture(t, 320, 320, true), 200}, {"too many pixels", previewFixture(t, 1600, 800, true), 200},
		{"oversized body", bytes.Repeat([]byte{'x'}, (2<<20)+1), 200}, {"HTTP failure", nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &countedBody{Reader: bytes.NewReader(original)}
			candidate := &countedBody{Reader: bytes.NewReader(tc.data)}
			calls := 0
			api := &API{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host == "i.redd.it" {
					return &http.Response{StatusCode: 200, Body: source}, nil
				}
				return &http.Response{StatusCode: tc.status, Body: candidate}, nil
			})}}
			result, gotW, gotH, err := api.artwork(context.Background(), "https://i.redd.it/source.png", []string{"https://preview.redd.it/image.png?width=320"})
			if err != nil || result != expected || gotW != w || gotH != h || calls != 2 || source.read != len(original) || candidate.read > (2<<20)+1 || !source.closed || !candidate.closed {
				t.Fatal("fallback changed behavior", err, calls, source.read, candidate.read)
			}
		})
	}
}

// Verify preview candidate and cancellation bounds
func TestPreviewCandidateAndCancellationBounds(t *testing.T) {
	for _, raw := range []string{"https://evil.invalid/a?width=320", "https://i.redd.it/a?width=320", "http://preview.redd.it/a?width=320", "https://preview.redd.it/a?width=1281", "https://preview.redd.it/a?width=319"} {
		if previewURL([]string{raw}) != "" {
			t.Fatal("unsafe/unsuitable preview selected", raw)
		}
	}
	candidates := make([]string, 8)
	candidates = append(candidates, "https://preview.redd.it/a?width=320")
	if previewURL(candidates) != "" {
		t.Fatal("candidate scan exceeded limit")
	}
	original := jpegPreviewFixture(t)
	source := &countedBody{Reader: bytes.NewReader(original)}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &API{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "i.redd.it" {
			return &http.Response{StatusCode: 200, Body: source}, nil
		}
		cancel()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("cancelled"))}, nil
	})}}
	if _, _, _, err := api.artwork(ctx, "https://i.redd.it/source.png", []string{"https://preview.redd.it/a?width=320"}); err != context.Canceled || calls != 2 || source.read > 64<<10 || !source.closed {
		t.Fatal("cancellation continued fallback", err, calls, source.read)
	}
	if _, _, _, err := api.artwork(ctx, "https://i.redd.it/source.png", nil); err != context.Canceled || calls != 2 {
		t.Fatal("cancelled call fetched", err, calls)
	}
}

// Verify preview does not bypass original image or request limits
func TestPreviewDoesNotBypassOriginalImageOrRequestLimits(t *testing.T) {
	for _, source := range [][]byte{[]byte("invalid original"), previewFixture(t, 8193, 1, false)} {
		calls := 0
		api := &API{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "i.redd.it" {
				t.Fatal("invalid original fetched preview")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(source))}, nil
		})}}
		if _, _, _, err := api.artwork(context.Background(), "https://i.redd.it/source.png", []string{"https://preview.redd.it/a?width=320"}); err == nil || calls != 1 {
			t.Fatal("invalid original accepted", err, calls)
		}
	}
	calls := 0
	api := &API{Endpoint: providerURL, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "meme-api.com" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"url":"https://i.redd.it/source.png","preview":["https://preview.redd.it/a?width=320"]}`))}, nil
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})}}
	if _, err := api.Fetch(context.Background(), "", false); err == nil || calls != 2 {
		t.Fatal("original request failure retried", err, calls)
	}
}

// Verify transparent original never trusts opaque provider preview
func TestTransparentOriginalNeverTrustsOpaqueProviderPreview(t *testing.T) {
	pngSource := previewFixture(t, 800, 400, true)
	palette := color.Palette{color.NRGBA{}, color.NRGBA{255, 0, 0, 255}}
	pixels := image.NewPaletted(image.Rect(0, 0, 800, 400), palette)
	for y := range 400 {
		for x := 400; x < 800; x++ {
			pixels.SetColorIndex(x, y, 1)
		}
	}
	var gifSource bytes.Buffer
	if err := gif.Encode(&gifSource, pixels, nil); err != nil {
		t.Fatal(err)
	}
	for _, source := range [][]byte{pngSource, gifSource.Bytes()} {
		expected, w, h, err := thumbnailSizeContext(context.Background(), source, 0, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		api := &API{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			data := source
			if r.URL.Host == "preview.redd.it" {
				data = previewFixture(t, 320, 160, false)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
		})}}
		actual, gotW, gotH, err := api.artwork(context.Background(), "https://i.redd.it/source", []string{"https://preview.redd.it/opaque.png?width=320"})
		if err != nil || actual != expected || gotW != w || gotH != h || calls != 1 {
			t.Fatal("opaque preview changed transparent original", err, calls)
		}
		data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(actual, "data:image/png;base64,"))
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, alpha := decoded.At(0, 0).RGBA()
		if alpha != 0 {
			t.Fatal("source transparency lost")
		}
	}
}

// Verify preview timeout preserves original fallback and near deadline skips
func TestPreviewTimeoutPreservesOriginalFallbackAndNearDeadlineSkips(t *testing.T) {
	original := jpegPreviewFixture(t)
	expected, _, _, err := thumbnailSizeContext(context.Background(), original, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, nearDeadline := range []bool{false, true} {
		ctx := context.Background()
		calls := 0
		previewCancelled := false
		api := &API{Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host == "i.redd.it" {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(original))}, nil
			}
			<-r.Context().Done()
			previewCancelled = true
			return nil, r.Context().Err()
		})}}
		if nearDeadline {
			api.Client.Timeout = 100 * time.Millisecond
		}
		result, _, _, err := api.artwork(ctx, "https://i.redd.it/source.jpg", []string{"https://preview.redd.it/a?width=320"})
		expectedCalls := 2
		if nearDeadline {
			expectedCalls = 1
		}
		if err != nil || result != expected || calls != expectedCalls || previewCancelled == nearDeadline {
			t.Fatal("optional preview consumed fallback deadline", nearDeadline, err, calls, previewCancelled)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if previewTimeAvailable(ctx, time.Now(), 0) || previewTimeAvailable(context.Background(), time.Now().Add(-14*time.Second), 15*time.Second) {
		t.Fatal("nearly expired source allowed preview")
	}
}
