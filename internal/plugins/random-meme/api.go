package randommeme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	artwork "github.com/StreamDockManager/Plugin-SDK/Go/thumbnail"
)

const providerURL = "https://meme-api.com/gimme"

var subredditName = regexp.MustCompile(`^[A-Za-z0-9_]{2,21}$`)

type Meme struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	PostLink      string `json:"postLink"`
	NSFW          bool   `json:"nsfw"`
	Spoiler       bool   `json:"spoiler"`
	png           []byte
	Image         string `json:"-"`
	Width, Height int    `json:"-"`
}

type API struct {
	Client   *http.Client
	Endpoint string
}

// Create the meme client with bounded timeouts and checked redirects
func newAPI() *API {
	// Validate every redirect destination instead of trusting the initial image origin
	return &API{Endpoint: providerURL, Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return imageURL(req.URL.String())
	}}}
}

// Allow only supported credential-free HTTPS image origins
func imageURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\t ") || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" {
		return fmt.Errorf("unsupported image URL")
	}
	switch u.Hostname() {
	case "i.redd.it", "preview.redd.it", "i.imgur.com":
		return nil
	}
	return fmt.Errorf("unsupported image host")
}

// Fetch response bytes within the caller-specified limit
func (a *API) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	body, err := a.open(ctx, address)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return readLimited(body, limit)
}

// Open a meme response while checking its HTTP status
func (a *API) open(ctx context.Context, address string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid meme request")
	}
	req.Header.Set("User-Agent", "stream-dock-manager-RandomMeme/0.1.1")
	response, err := a.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meme provider request failed")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("meme provider returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

// Reject response bodies larger than the permitted byte limit
func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("could not read meme response")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("meme response exceeds size limit")
	}
	return data, nil
}

// Fetch is called only by a Fetch button press. One deadline bounds all attempts
func (a *API) Fetch(ctx context.Context, subreddit string, allowNSFW bool) (*Meme, error) {
	endpoint := a.Endpoint
	if subreddit != "" {
		if !subredditName.MatchString(subreddit) {
			return nil, fmt.Errorf("use a subreddit name without r/ or spaces")
		}
		endpoint += "/" + subreddit
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for range 3 {
		data, err := a.get(ctx, endpoint, 64<<10)
		if err != nil {
			return nil, err
		}
		var response struct {
			Meme
			Preview []string `json:"preview"`
		}
		if json.Unmarshal(data, &response) != nil || response.URL == "" {
			return nil, fmt.Errorf("malformed meme response")
		}
		meme := response.Meme
		if meme.NSFW && !allowNSFW || meme.Spoiler {
			continue
		}
		if imageURL(meme.URL) != nil {
			continue
		}
		meme.Image, meme.Width, meme.Height, err = a.artwork(ctx, meme.URL, response.Preview)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if requestErr, ok := errors.AsType[*artworkRequestError](err); ok {
				return nil, requestErr.error
			}
			continue
		}
		meme.Title = string([]rune(meme.Title)[:min(len([]rune(meme.Title)), 200)])
		return &meme, nil
	}
	return nil, fmt.Errorf("no image matching your content settings was found; try Fetch again")
}

// Validate decoded image dimensions against the resource budget
func validDimensions(cfg image.Config) bool {
	return cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 8192 && cfg.Height <= 8192 && int64(cfg.Width)*int64(cfg.Height) <= artwork.MaxSourcePixels
}

// Calculate bounded aspect-preserving thumbnail dimensions
func thumbnailDimensions(cfg image.Config) (int, int) {
	w, h, _ := artwork.Fit(cfg.Width, cfg.Height, 320, 320)
	return w, h
}

// Rasterize a bounded first frame; never wrap raster data in SVG. The source
// link stays unchanged for pasting/embeds. Thumbnails fit the 1 MiB wire limit
func thumbnailSizeContext(ctx context.Context, data []byte, w, h int, requireOpaque bool) (string, int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validDimensions(cfg) {
		return "", 0, 0, fmt.Errorf("unsupported meme image dimensions")
	}
	if w == 0 || h == 0 {
		w, h = thumbnailDimensions(cfg)
	}
	var source image.Image
	if requireOpaque {
		// Optional previews already have a 1MP admission limit. Inspect every pixel
		// for opacity: opacity of only the selected samples cannot establish that
		// a provider preserved an opaque original. Keep this bounded validation
		if int64(cfg.Width)*int64(cfg.Height) > 1_000_000 {
			return "", 0, 0, fmt.Errorf("preview exceeds image limit")
		}
		source, _, err = image.Decode(bytes.NewReader(data))
	} else {
		source, err = artwork.Decode(ctx, data, w, h)
	}
	if err != nil {
		return "", 0, 0, fmt.Errorf("unsupported meme image: %w", err)
	}
	if !requireOpaque {
		uri, err := artwork.DataURI(ctx, source)
		return uri, w, h, err
	}
	opaque, ok := source.(interface{ Opaque() bool })
	if !ok || !opaque.Opaque() {
		return "", 0, 0, fmt.Errorf("preview changed image opacity")
	}
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, w, h))
	bounds := source.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			thumb.Set(x, y, source.At(bounds.Min.X+x*cfg.Width/w, bounds.Min.Y+y*cfg.Height/h))
		}
	}
	uri, err := artwork.DataURI(ctx, thumb)
	return uri, w, h, err
}
