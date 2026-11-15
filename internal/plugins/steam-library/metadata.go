package steamlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

type steamApp struct {
	id, name, install string
	flags             uint64
}
type steamFileData struct {
	libraries []string
	app       *steamApp
	playtime  map[string]int
	installed bool
}

// Retain only the Steam metadata fields needed for discovery
func compactSteamMetadata(path string, data VDF) steamFileData {
	var compact steamFileData
	switch filepath.Base(path) {
	case "libraryfolders.vdf":
		folders := vdfObject(data["libraryfolders"])
		keys := make([]string, 0, len(folders))
		for key := range folders {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := folders[key]
			path := vdfString(vdfObject(value), "path")
			if path == "" {
				path, _ = value.(string)
			}
			if path != "" {
				compact.libraries = append(compact.libraries, path)
			}
		}
	case "localconfig.vdf":
		apps := vdfObject(vdfObject(vdfObject(vdfObject(vdfObject(data["UserLocalConfigStore"])["Software"])["Valve"])["Steam"])["apps"])
		compact.playtime = map[string]int{}
		for id, item := range apps {
			if _, err := strconv.ParseUint(id, 10, 32); err != nil {
				continue
			}
			if minutes, err := strconv.Atoi(vdfString(vdfObject(item), "Playtime")); err == nil {
				compact.playtime[id] = minutes
			}
		}
	default:
		app := vdfObject(data["AppState"])
		id := vdfString(app, "appid")
		if _, err := strconv.ParseUint(id, 10, 32); err == nil {
			flags, _ := strconv.ParseUint(vdfString(app, "StateFlags"), 10, 32)
			compact.app = &steamApp{id: id, name: vdfString(app, "name"), install: vdfString(app, "installdir"), flags: flags}
		}
	}
	return compact
}

// Determine whether parsed metadata describes an installed game
func steamInstalled(path string, app *steamApp) bool {
	if app == nil || app.flags&4 == 0 || app.install == "" || filepath.Base(app.install) != app.install {
		return false
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(path), "common", app.install))
	return err == nil && info.IsDir()
}

// Account for compact string storage with headroom, map slots, file identity and
// record overhead. Source-file bytes are no longer used as a parsed-heap proxy
func steamMetadataBytes(path string, data steamFileData) int64 {
	size := int64(512 + 2*len(path) + 24*cap(data.libraries))
	for _, path := range data.libraries {
		size += int64(2 * len(path))
	}
	if app := data.app; app != nil {
		size += int64(128 + 2*(len(app.id)+len(app.name)+len(app.install)))
	}
	for id := range data.playtime {
		size += int64(128 + 2*len(id))
	}
	return size
}

type steamGameRef struct {
	app     *steamApp
	library string
}
type steamScan struct {
	games    []steamGameRef
	playtime map[string]int
}

// Scan configured Steam libraries using the supplied bounded reader
func scanSteam(ctx context.Context, root, account string, read func(string) (steamFileData, error)) (steamScan, error) {
	root = steamRoot(root)
	libraries := []string{root}
	if data, err := read(filepath.Join(root, "steamapps/libraryfolders.vdf")); err == nil {
		libraries = append(libraries, data.libraries...)
	}
	var scan steamScan
	if account != "" {
		if _, err := strconv.ParseUint(account, 10, 64); err != nil {
			return scan, fmt.Errorf("Steam account must be numeric")
		}
		if data, err := read(filepath.Join(root, "userdata", account, "config/localconfig.vdf")); err == nil {
			scan.playtime = data.playtime
		}
	}
	seen := map[string]bool{}
	for _, library := range libraries {
		files, _ := filepath.Glob(filepath.Join(library, "steamapps/appmanifest_*.acf"))
		for _, path := range files {
			if err := ctx.Err(); err != nil {
				return scan, err
			}
			data, err := read(path)
			if err != nil || data.app == nil || !data.installed || seen[data.app.id] {
				continue
			}
			scan.games = append(scan.games, steamGameRef{data.app, library})
			seen[data.app.id] = true
		}
	}
	if err := ctx.Err(); err != nil {
		return scan, err
	}
	if len(scan.games) == 0 {
		return scan, fmt.Errorf("no installed Steam games found; configure the Steam folder")
	}
	return scan, nil
}

// Build the discovered game inventory and its logical byte charge
func (scan steamScan) inventory() ([]SteamGame, int64) {
	games := make([]SteamGame, len(scan.games))
	bytes := int64(128 * len(games))
	for i, ref := range scan.games {
		game := SteamGame{ID: ref.app.id, Name: ref.app.name, Library: ref.library}
		if minutes, ok := scan.playtime[game.ID]; ok {
			game.PlaytimeMinutes = &minutes
			bytes += 8
		}
		games[i] = game
		bytes += int64(2 * (len(game.ID) + len(game.Name) + len(game.Library)))
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Name < games[j].Name })
	return games, bytes
}
