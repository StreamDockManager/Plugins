package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/StreamDockManager/Plugin-SDK/Go/packaging"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

type noticeModule = packaging.Module

// dependencyModules enumerates modules whose licenses accompany the package
func (builder *buildRunner) dependencyModules(d official.Definition, osname string, archs []string) ([]noticeModule, error) {
	modules := map[string]noticeModule{}
	for _, arch := range archs {
		analysis, err := builder.dependencies(d.Slug, osname, arch, false)
		if err != nil {
			return nil, err
		}
		for _, module := range analysis.modules {
			modules[module.Path+"@"+module.Version+"@"+module.Dir] = module
		}
	}
	result := make([]noticeModule, 0, len(modules))
	for _, module := range modules {
		result = append(result, module)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// dependencyNotices collects notices for the resolved dependency modules
func (builder *buildRunner) dependencyNotices(d official.Definition, osname string, archs []string) ([]byte, error) {
	modules, err := builder.dependencyModules(d, osname, archs)
	if err != nil {
		return nil, err
	}
	data, err := builder.moduleNotices(modules)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(data)
	analysis, err := builder.dependencies(d.Slug, osname, archs[0], false)
	if err != nil {
		return nil, err
	}
	goroot := ""
	for _, pkg := range analysis.packages {
		if pkg.ImportPath == "runtime" {
			goroot = filepath.Dir(filepath.Dir(pkg.Dir))
			break
		}
	}
	if goroot == "" {
		return nil, fmt.Errorf("Go runtime missing from dependency analysis")
	}
	data, err = os.ReadFile(filepath.Join(goroot, "LICENSE"))
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&out, "\n===== Go runtime and standard library =====\n%s\n", data)

	return out.Bytes(), nil
}

// packageNotices assembles the notices shipped with this package
func packageNotices(d official.Definition) []byte {
	text := "# Dependency notices\n\nThis package contains only the runtime dependency licenses collected in THIRD_PARTY_LICENSES.txt. The project license is in LICENSE. Build-only tools are not distributed.\n"
	if d.Slug == "weather" {
		text += "\nLocation search: Nominatim / OpenStreetMap contributors, ODbL 1.0 (https://www.openstreetmap.org/copyright). Service policy: https://operations.osmfoundation.org/policies/nominatim/.\nWeather data: Open-Meteo, CC BY 4.0 (https://open-meteo.com/). The free endpoint is restricted to noncommercial usage under https://open-meteo.com/en/terms. Paid/self-hosted endpoints are supported.\n"
	}
	if d.Slug == "spotify" {
		text += "\nSpotify is a third-party application and trademark. No Spotify Web API credentials are used or distributed.\n"
	}

	switch d.Slug {
	case "obs-studio", "home-assistant", "vlc", "tailscale", "mqtt", "make", "osrs-hiscores", "runescape-hiscores", "retroachievements":
		text += "\nExternal applications/services and their credentials are not distributed. Package and action SVG glyphs are original geometry.\n"
	}
	if d.Slug == "osrs-hiscores" || d.Slug == "runescape-hiscores" {
		text += "\nHiscores data: Jagex, https://secure.runescape.com/. No RuneLite companion or game-account credentials are used.\n"
		text += "\nGame skill and cape artwork © Jagex Limited; supplied through the OSRS and RuneScape Wikis. Per-image source URLs and hashes are in hiscores/artwork-sources.txt. Game artwork is separately owned and is not relicensed under the project's GPL license.\nCreated using intellectual property belonging to Jagex Limited under the terms of Jagex's Fan Content Policy. This content is not endorsed by or affiliated with Jagex.\nhttps://legal.jagex.com/docs/policies/fan-content-policy\n"
	}
	if d.Slug == "retroachievements" {
		text += "\nRetroAchievements Web API, https://api-docs.retroachievements.org/. Web API key is supplied by the user.\n"
	}
	return []byte(text)
}
