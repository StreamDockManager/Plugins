package steamlibrary

import (
	"context"
	"errors"
	"fmt"
	cache "github.com/StreamDockManager/Plugin-SDK/Go/cache"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

type steamMetadata struct {
	info  os.FileInfo
	data  steamFileData
	bytes int64
}

// Parsed metadata is private and read-only. Every browse validates directory
// membership and file identity; warm navigation only stats unchanged manifests
type steamIndex struct {
	once              sync.Once
	gate              chan struct{}
	files             *cache.Cache[steamMetadata]
	read              func(string) (VDF, error)
	revision          uint64
	inventoryRevision uint64
	inventory         []SteamGame
	inventoryBytes    int64
	root, account     string
}

const steamIndexByteLimit = 32 << 20
const steamIndexEntryLimit = 8192

// Inspect Steam metadata without following unsafe file forms
func statSteamMetadata(path string) (os.FileInfo, error) {
	if runtime.GOOS != "windows" {
		return os.Stat(path)
	}
	// Windows path-based Stat defers file identity lookup until SameFile. Capture
	// it from a handle now, before a reader can replace the path, and close the
	// handle before reading so it cannot prevent a concurrent replacement
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	return info, errors.Join(err, file.Close())
}

// Invalidate the cached Steam inventory after metadata changes
func (index *steamIndex) changed() {
	index.revision++
	index.inventory = nil
	index.inventoryBytes = 0
}

// Remove one cached Steam metadata entry and its byte charge
func (index *steamIndex) forget(path string) {
	if index.files.Delete(path) {
		index.changed()
	}
}

// Evict Steam metadata until the incoming entry fits the byte budget
func (index *steamIndex) makeRoom(bytes int64) {
	removed, _ := index.files.Trim(int(bytes+index.inventoryBytes), 0)
	if len(removed) > 0 {
		index.changed()
	}
}

// Callers share the returned immutable inventory, including playtime pointers
// Filtering must use a separate slice; changes publish a new inventory
func (index *steamIndex) discover(ctx context.Context, root, account string) ([]SteamGame, error) {
	index.once.Do(func() { index.gate = make(chan struct{}, 1) })
	select {
	case index.gate <- struct{}{}:
		defer func() { <-index.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if index.files == nil {
		index.files, _ = cache.New[steamMetadata](steamIndexEntryLimit, steamIndexByteLimit)
	}
	seen := map[string]bool{}
	// Reuse compact metadata only while file identity and modification data still match
	read := func(path string) (steamFileData, error) {
		if err := ctx.Err(); err != nil {
			return steamFileData{}, err
		}
		path = filepath.Clean(path)
		seen[path] = true
		info, err := statSteamMetadata(path)
		if err != nil {
			index.forget(path)
			return steamFileData{}, err
		}
		if entry, ok := index.files.Get(path, 0); ok && os.SameFile(info, entry.info) && info.Size() == entry.info.Size() && info.ModTime().Equal(entry.info.ModTime()) {
			installed := steamInstalled(path, entry.data.app)
			if installed != entry.data.installed {
				index.changed()
				entry.data.installed = installed
				_, _ = index.files.Put(path, entry, int(entry.bytes), 0, 0)
			}
			return entry.data, nil
		}
		index.forget(path)
		load := index.read
		if load == nil {
			load = readVDF
		}
		parsed, err := load(path)
		if err != nil {
			return steamFileData{}, err
		}
		if err = ctx.Err(); err != nil {
			return steamFileData{}, err
		}
		after, err := statSteamMetadata(path)
		if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return steamFileData{}, fmt.Errorf("Steam metadata changed during reading")
		}
		data := compactSteamMetadata(path, parsed)
		data.installed = steamInstalled(path, data.app)
		bytes := steamMetadataBytes(path, data)
		index.changed()
		if bytes <= steamIndexByteLimit {
			_, _ = index.files.Put(path, steamMetadata{info: info, data: data, bytes: bytes}, int(bytes), 0, 0)
		}
		return data, nil
	}
	scan, err := scanSteam(ctx, root, account, read)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	entries, _ := index.files.Entries(0)
	for _, entry := range entries {
		path := entry.Key
		if !seen[path] {
			index.forget(path)
		}
	}
	if err != nil {
		index.changed()
		return nil, err
	}
	if index.inventory != nil && index.inventoryRevision == index.revision && index.root == root && index.account == account {
		return index.inventory, nil
	}
	games, bytes := scan.inventory()
	index.inventory = nil
	index.inventoryBytes = 0
	if bytes <= steamIndexByteLimit {
		index.makeRoom(bytes)
		index.inventory = games
		index.inventoryBytes = bytes
		index.inventoryRevision = index.revision
		index.root, index.account = root, account
	}
	return games, nil
}

// Recheck that the selected Steam game is still installed
func validateSteamInstallation(ctx context.Context, game SteamGame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := readVDF(filepath.Join(game.Library, "steamapps", "appmanifest_"+game.ID+".acf"))
	if err != nil {
		return err
	}
	app := vdfObject(data["AppState"])
	flags, _ := strconv.ParseUint(vdfString(app, "StateFlags"), 10, 32)
	if vdfString(app, "appid") != game.ID || flags&4 == 0 {
		return os.ErrNotExist
	}
	install := vdfString(app, "installdir")
	if install == "" || filepath.Base(install) != install {
		return os.ErrNotExist
	}
	st, err := os.Stat(filepath.Join(game.Library, "steamapps/common", install))
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return os.ErrNotExist
	}
	return nil
}
