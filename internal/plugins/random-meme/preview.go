package randommeme

import (
	"bytes"
	"context"
	artwork "github.com/StreamDockManager/Plugin-SDK/Go/thumbnail"
	"image"
	"io"
	"net/url"
	"strconv"
	"time"
)

type artworkRequestError struct{ error }

// Provider previews are ordered by quality, but width hints are only selection
// hints. Validate decoded dimensions against the original before using one
// Inspect eight URLs and fetch at most one preview for each candidate meme
func previewURL(previews []string) string {
	selected, width := "", 1281
	for _, raw := range previews[:min(len(previews), 8)] {
		if imageURL(raw) != nil {
			continue
		}
		u, _ := url.Parse(raw)
		candidate, err := strconv.Atoi(u.Query().Get("width"))
		if u.Hostname() == "preview.redd.it" && err == nil && candidate >= 320 && candidate < width {
			selected, width = raw, candidate
		}
	}
	return selected
}

// Prepare bounded artwork for a device display
func (a *API) artwork(ctx context.Context, original string, previews []string) (string, int, int, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	preview := previewURL(previews)
	if preview == "" {
		data, err := a.get(ctx, original, artwork.MaxInputBytes)
		if err != nil {
			return "", 0, 0, &artworkRequestError{err}
		}
		return thumbnailContext(ctx, data, 0, 0, false)
	}
	started := time.Now()
	body, err := a.open(ctx, original)
	if err != nil {
		return "", 0, 0, &artworkRequestError{err}
	}
	defer body.Close()
	// Keep only a bounded original header while checking aspect/format. If the
	// preview fails, replay the prefix into the same response without another GET
	var prefix bytes.Buffer
	config, format, configErr := image.DecodeConfig(io.TeeReader(io.LimitReader(body, 64<<10), &prefix))
	// A preview's file format cannot prove that it preserved source alpha
	// Only JPEG is known opaque here; PNG/GIF sample the original image instead
	if configErr == nil && format == "jpeg" && validDimensions(config) && max(config.Width, config.Height) > 320 && ctx.Err() == nil && previewTimeAvailable(ctx, started, a.Client.Timeout) {
		previewContext, cancelPreview := context.WithTimeout(ctx, time.Second)
		data, previewErr := a.get(previewContext, preview, 2<<20)
		cancelPreview()
		if previewErr == nil {
			candidate, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
			w, h := thumbnailDimensions(config)
			cross := int64(candidate.Width)*int64(config.Height) - int64(candidate.Height)*int64(config.Width)
			if cross < 0 {
				cross = -cross
			}
			// Preserve the original aspect to one source-pixel rounding step
			// Never upscale a preview smaller than the required output
			if decodeErr == nil && validDimensions(candidate) && int64(candidate.Width)*int64(candidate.Height) <= 1_000_000 && candidate.Width >= w && candidate.Height >= h && cross <= int64(max(config.Width, config.Height)) && ctx.Err() == nil {
				if result, width, height, err := thumbnailContext(ctx, data, w, h, true); err == nil {
					return result, width, height, nil
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	data, err := readLimited(io.MultiReader(bytes.NewReader(prefix.Bytes()), body), artwork.MaxInputBytes)
	if err != nil {
		return "", 0, 0, &artworkRequestError{err}
	}
	return thumbnailContext(ctx, data, 0, 0, false)
}

// Optional work must not use the original response's entire timeout. Give a
// preview at most one second and reserve at least one second for fallback
func previewTimeAvailable(ctx context.Context, started time.Time, clientTimeout time.Duration) bool {
	deadline, hasDeadline := ctx.Deadline()
	if clientTimeout > 0 {
		clientDeadline := started.Add(clientTimeout)
		if !hasDeadline || clientDeadline.Before(deadline) {
			deadline, hasDeadline = clientDeadline, true
		}
	}
	return !hasDeadline || time.Until(deadline) >= 2*time.Second
}

// Create a bounded thumbnail and reject results produced after cancellation
func thumbnailContext(ctx context.Context, data []byte, w, h int, requireOpaque bool) (string, int, int, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	result, width, height, err := thumbnailSizeContext(ctx, data, w, h, requireOpaque)
	if ctx.Err() != nil {
		return "", 0, 0, ctx.Err()
	}
	return result, width, height, err
}
