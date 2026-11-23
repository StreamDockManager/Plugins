package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/cache"
)

// Vendor setImage accepts data URIs. Read only artwork declared by this plugin,
// resolving from its package even when a universal ZIP nests the executable.
type packageImages struct {
	root    string
	types   map[string]string
	encoded *cache.Cache[string]
}

func installedImages(def Definition, visuals map[string]Visual) *packageImages {
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	root := filepath.Dir(executable)
	for !strings.HasSuffix(strings.ToLower(root), ".sdplugin") {
		parent := filepath.Dir(root)
		if parent == root {
			return nil
		}
		root = parent
	}
	return newPackageImages(root, def, visuals)
}

func newPackageImages(root string, def Definition, visuals map[string]Visual) *packageImages {
	encoded, _ := cache.New[string](128, 4<<20)
	images := &packageImages{root: root, types: map[string]string{}, encoded: encoded}
	add := func(name string) {
		if name == "" {
			return
		}
		switch filepath.Ext(name) {
		case "":
			images.types[name+".svg"] = "image/svg+xml"
		case ".svg":
			images.types[name] = "image/svg+xml"
		case ".png":
			images.types[name] = "image/png"
		}
	}
	add("icons/unavailable")
	for _, name := range def.RuntimeAssets {
		add(name)
	}
	for _, visual := range visuals {
		add(visual.Icon)
		for _, name := range visual.States {
			add(name)
		}
	}
	return images
}

func (images *packageImages) image(name string) string {
	if strings.HasPrefix(name, "data:") {
		return name
	}
	mediaType, ok := images.types[name]
	if !ok {
		return name
	}
	if encoded, ok := images.encoded.Get(name, 0); ok {
		return encoded
	}
	file, err := os.OpenInRoot(images.root, name)
	if err != nil {
		return name
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, sdk.MaxPortableImageBytes+1))
	if err != nil {
		return name
	}
	encoded, err := sdk.ImageDataURI(mediaType, data)
	if err != nil {
		return name
	}
	_, _ = images.encoded.Put(name, encoded, len(encoded), 0, 0)
	return encoded
}

// PortableImage applies the same host policy to plugins with a custom renderer.
func PortableImage(e *Engine, name string) string {
	if state, ok := managedStates.Load(e); ok {
		if images := state.(*managedState).images; images != nil {
			return images.image(name)
		}
	}
	return name
}
