package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/StreamDockManager/Plugins/internal/hiscores"
)

//go:embed artwork/hiscores/*/*.png artwork/hiscores/sources.json
var hiscoresArtwork embed.FS

// Package the original raster artwork directly. Manager deliberately ignores
// embedded images inside SVGs, so SVG wrappers would render as blank artwork
func HiscoresAssets(slug string) map[string][]byte {
	game := strings.TrimSuffix(slug, "-hiscores")
	files := map[string][]byte{}
	entries, err := fs.ReadDir(hiscoresArtwork, "artwork/hiscores/"+game)
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		data, err := hiscoresArtwork.ReadFile("artwork/hiscores/" + game + "/" + entry.Name())
		if err != nil {
			panic(err)
		}
		files["hiscores/"+game+"/"+entry.Name()] = data
	}
	files["hiscores/artwork-sources.txt"], _ = hiscoresArtwork.ReadFile("artwork/hiscores/sources.json")
	for _, wide := range []bool{false, true} {
		shape := "key"
		if wide {
			shape = "wide"
		}
		for _, variant := range []string{"level", "detail", "number"} {
			cape := hiscores.ArtworkRect(variant, wide)
			value, name, extra := [4]int{2, 60, 196, 25}, [4]int{2, 86, 196, 13}, [4]int{0, 0, 0, 0}
			font := 18
			if variant != "level" {
				font = 12
				value, name, extra = [4]int{2, 42, 196, 22}, [4]int{2, 66, 196, 13}, [4]int{2, 80, 196, 19}
			}
			if variant == "number" {
				font = 8
			}
			if wide {
				if variant == "number" {
					font = 12
				}
				value, name, extra = [4]int{78, 34, 120, 32}, [4]int{78, 12, 120, 18}, [4]int{78, 70, 120, 27}
			}
			text := func(key string, rect [4]int, size int) map[string]any {
				return map[string]any{"key": key, "type": "text", "rect": rect, "font": map[string]any{"size": size, "fit": true}}
			}
			layout := map[string]any{"id": "hiscores-" + variant + "-" + shape, "items": []map[string]any{
				{"key": "cape", "type": "pixmap", "rect": cape}, text("value", value, font), text("name", name, 9), text("detail", extra, 6), text("status", [4]int{140, 1, 58, 12}, 6),
			}}
			data, _ := json.Marshal(layout)
			files[fmt.Sprintf("hiscores/%s-%s.json", variant, shape)] = data
		}
	}
	return files
}
