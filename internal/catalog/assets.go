package catalog

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/StreamDockManager/Plugin-SDK/Go/packaging"
)

// RequiredAssets includes manifest visuals and paths chosen by runtime states
func RequiredAssets(d Definition) []string {
	required := map[string]bool{"icons/unavailable": true}
	for _, path := range d.RuntimeAssets {
		required[path] = true
	}
	for _, action := range d.Actions {
		visual := VisualFor(d.Slug, action)
		required[visual.Icon] = true
		for _, path := range visual.States {
			required[path] = true
		}
	}
	result := []string{}
	for path := range required {
		if path != "" {
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

// ValidateAssets rejects missing or invalid assets required by the registered actions
func ValidateAssets(d Definition, files map[string][]byte) error {
	for _, path := range append(RequiredAssets(d), "icon") {
		suffixes := []string{".svg", "-light.svg", "-dark.svg"}
		if explicitAsset(path) {
			suffixes = []string{""}
		}
		for _, suffix := range suffixes {
			if _, ok := files[path+suffix]; !ok {
				return fmt.Errorf("%s: required asset missing: %s", d.Slug, path+suffix)
			}
		}
	}
	for _, name := range []string{"display/key", "display/wide"} {
		for _, suffix := range []string{".json", "-light.json", "-dark.json"} {
			if _, ok := files[name+suffix]; !ok {
				return fmt.Errorf("%s: required layout missing: %s", d.Slug, name+suffix)
			}
		}
	}
	for name, data := range files {
		if strings.HasSuffix(name, ".svg") {
			decoder := xml.NewDecoder(strings.NewReader(string(data)))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("%s: invalid SVG %s: %w", d.Slug, name, err)
				}
				if element, ok := token.(xml.StartElement); ok && (element.Name.Local == "image" || element.Name.Local == "foreignObject") {
					return fmt.Errorf("%s: %s contains SVG %s content unsupported by the native renderer; package raster artwork directly", d.Slug, name, element.Name.Local)
				}
			}
		}
		if strings.HasSuffix(name, ".json") {
			var layout any
			if err := json.Unmarshal(data, &layout); err != nil {
				return fmt.Errorf("%s: invalid layout %s: %w", d.Slug, name, err)
			}
			if err := packaging.ValidateLayoutReferences(layout, files); err != nil {
				return fmt.Errorf("%s: %s: %w", d.Slug, name, err)
			}
		}
		if strings.HasPrefix(name, "spotify/") && d.Slug != "spotify" {
			return fmt.Errorf("unrelated Spotify asset in %s", d.Slug)
		}
	}
	return nil
}

// Extensionless visual references use themed SVG variants. Explicit file paths,
// such as game artwork PNGs, keep their original format and colors in each theme
func explicitAsset(name string) bool { return path.Ext(name) != "" }
