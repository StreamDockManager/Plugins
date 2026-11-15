package randommeme

import (
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"strings"

	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

var Icons = map[string]string{"fetch": "cycle", "send": "request", "copy": "copy"}
var Definition, visuals = c.Define("random-meme", "Random Meme", Icons, "fetch", "send", "copy")

// Declare the random meme global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Check("allow_nsfw", "Allow NSFW images")}
}

// A caption retains a surface so it stays readable over a user backdrop
const CaptionAsset = "display/meme-caption.svg"
const CaptionSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100" preserveAspectRatio="none"><path fill="#0f141d" d="M0 0h200v100H0z"/></svg>`

// Declare meme runtime assets and explicitly shared view targets
func init() {
	Definition.RuntimeAssets = []string{"display/meme.json", CaptionAsset}
	for i := range Definition.ViewTargets {
		Definition.ViewTargets[i].Shared = true
	}
}

// Declare settings supported by the selected random meme action
func ActionFields(action string) []settings.Field {
	fields := []settings.Field{metadata.Text("shared_id", "Meme group (match Fetch, Copy, and Paste)", "memes")}
	if action == "fetch" {
		fields = append(fields, metadata.Text("subreddit", "Subreddit (optional, without r/)", ""))
	}
	return fields
}

// Return the packaged meme display layout
func Layout() map[string]any {
	return map[string]any{"id": "random-meme", "items": []map[string]any{
		{"key": "icon", "label": "Action icon", "type": "pixmap", "rect": [4]int{4, 82, 36, 18}},
		{"key": "meme", "label": "Meme image", "type": "pixmap", "rect": [4]int{0, 0, 200, 82}},
		{"key": "caption-background", "streamDockManager": map[string]any{"role": "background"}, "label": "Caption backdrop", "type": "pixmap", "rect": [4]int{0, 82, 200, 18}},
		{"key": "status", "label": "Status", "type": "text", "rect": [4]int{44, 82, 152, 18}, "font": map[string]any{"size": 17, "fit": true}},
	}}
}

// View keeps stable fields for Manager's layout editor and contains the whole
// image at the binding's actual surface aspect ratio
func View(b engine.Binding, meme *Meme, caption, theme string) engine.Display {
	image := "icons/cycle"
	if engine.Action(b.Action) == "send" {
		image = "icons/request"
	}
	if engine.Action(b.Action) == "copy" {
		image = "icons/copy"
	}
	image += ".svg"
	if theme != "" {
		image = strings.TrimSuffix(image, ".svg") + "-" + theme + ".svg"
	}
	icon := image
	w, h, iw, ih := 112, 112, 32, 32
	if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
		w, h = b.Surface.Width, b.Surface.Height
	}
	if meme != nil {
		image, iw, ih = meme.Image, meme.Width, meme.Height
	}
	width, height := 200., 82.
	if float64(iw)/float64(ih) > float64(w)/(float64(h)*.82) {
		height = float64(ih) * float64(w) * 100 / (float64(iw) * float64(h))
	} else {
		width = float64(iw) * float64(h) * 82 / (float64(ih) * float64(w))
	}
	layout := "display/meme.json"
	if theme != "" {
		layout = "display/meme-" + theme + ".json"
	}
	return engine.Display{Layout: layout, Image: image, Title: caption, Feedback: map[string]any{
		"meme":               map[string]any{"value": image, "rect": [4]float64{(200 - width) / 2, (82 - height) / 2, width, height}},
		"icon":               map[string]any{"value": icon, "zOrder": 1},
		"caption-background": captionAsset(theme),
		"status":             caption,
	}}
}

// Select the meme caption asset for the current presentation
func captionAsset(theme string) string {
	if theme == "" {
		return CaptionAsset
	}
	return strings.TrimSuffix(CaptionAsset, ".svg") + "-" + theme + ".svg"
}
