package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed device-palettes.v1.json
var devicePaletteJSON []byte

var devicePalettes = func() map[string]map[string]map[string]string {
	var data struct {
		Palettes map[string]map[string]map[string]string
	}
	if err := json.Unmarshal(devicePaletteJSON, &data); err != nil {
		panic(err)
	}
	return data.Palettes
}()

// markLayoutRoles annotates layout elements with semantic palette roles
func markLayoutRoles(data []byte) []byte {
	var layout map[string]any
	if json.Unmarshal(data, &layout) != nil {
		return data
	}
	metadata, ok := layout["streamDockManager"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
		layout["streamDockManager"] = metadata
	}
	metadata["displayPalette"] = true
	result, _ := json.Marshal(layout)
	return result
}

var roleAttribute = regexp.MustCompile(` (fill|stroke)="(#0f141d|#080808|#f5f7fa|#fafafa)"`)

// markIconRoles annotates icon elements with semantic palette roles
func markIconRoles(data []byte) []byte {
	return roleAttribute.ReplaceAllFunc(data, func(attribute []byte) []byte {
		parts := roleAttribute.FindSubmatch(attribute)
		role := "accent"
		if string(parts[2]) == "#0f141d" || string(parts[2]) == "#080808" {
			role = "surface"
		}
		return fmt.Appendf(attribute[:len(attribute):len(attribute)], ` data-sdm-%s="%s"`, parts[1], role)
	})
}

// PreviewPaletteAsset applies the declared roles for offline contact sheets
func PreviewPaletteAsset(path, mode, palette string, data []byte) []byte {
	colors := devicePalettes[mode][palette]
	if colors == nil {
		return data
	}
	switch {
	case strings.HasSuffix(path, ".svg"):
		return previewRoleAttribute.ReplaceAllFunc(data, func(attribute []byte) []byte {
			parts := previewRoleAttribute.FindSubmatch(attribute)
			return fmt.Appendf(nil, ` %s="%s"`, parts[1], colors[string(parts[2])])
		})
	case strings.HasSuffix(path, ".json"):
		var layout map[string]any
		if json.Unmarshal(data, &layout) != nil {
			return data
		}
		layout["background"] = colors["surface"]
		items, _ := layout["items"].([]any)
		for _, entry := range items {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			switch item["type"] {
			case "text":
				role := "ink"
				switch item["key"] {
				case "line1", "line2", "label", "artist", "detail", "name":
					role = "muted"
				}
				item["color"] = colors[role]
			case "bar":
				item["bar_fill_c"], item["bar_bg_c"] = colors["meter"], colors["border"]
			}
		}
		result, _ := json.Marshal(layout)
		return result
	default:
		return data
	}
}

var previewRoleAttribute = regexp.MustCompile(` (fill|stroke)="#[a-fA-F0-9]+" data-sdm-(?:fill|stroke)="(accent|surface)"`)
