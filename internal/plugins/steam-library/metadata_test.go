package steamlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Create disposable Steam metadata for inventory tests
func syntheticSteamLibrary(t *testing.T, games, unusedFields, nameBytes int) string {
	t.Helper()
	root := t.TempDir()
	apps := filepath.Join(root, "steamapps")
	for i := 1; i <= games; i++ {
		install := fmt.Sprintf("Fixture-%04d", i)
		if err := os.MkdirAll(filepath.Join(apps, "common", install), 0700); err != nil {
			t.Fatal(err)
		}
		name := install
		if nameBytes > 0 {
			name = strings.Repeat("x", nameBytes) + install
		}
		var body strings.Builder
		fmt.Fprintf(&body, `"AppState" { "appid" "%d" "name" "%s" "StateFlags" "4" "installdir" "%s" "Unused" {`, i, name, install)
		for j := range unusedFields {
			fmt.Fprintf(&body, `"unused%04d" "%s" `, j, strings.Repeat("x", 128))
		}
		body.WriteString(`}}`)
		if err := os.WriteFile(filepath.Join(apps, fmt.Sprintf("appmanifest_%d.acf", i)), []byte(body.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Verify compact steam metadata and shared warm inventory
func TestCompactSteamMetadataAndSharedWarmInventory(t *testing.T) {
	root := syntheticSteamLibrary(t, 500, 64, 0)
	reads := 0
	index := steamIndex{read: func(path string) (VDF, error) { reads++; return readVDF(path) }}
	first, err := index.discover(context.Background(), root, "")
	if err != nil || len(first) != 500 {
		t.Fatal(err, len(first))
	}
	stringsBytes := 0
	entries, _ := index.files.Entries(0)
	for _, cached := range entries {
		entry := cached.Value
		if entry.data.app == nil || len(entry.data.playtime) != 0 || len(entry.data.libraries) != 0 {
			t.Fatal("unexpected compact record")
		}
		app := entry.data.app
		stringsBytes += len(app.id) + len(app.name) + len(app.install)
	}
	allocs := testing.AllocsPerRun(3, func() {
		warm, err := index.discover(context.Background(), root, "")
		if err != nil || len(warm) != 500 || &warm[0] != &first[0] {
			t.Fatal("unchanged inventory rebuilt", err)
		}
	})
	if reads != 500 || stringsBytes >= 100000 || int64(index.files.Bytes())+index.inventoryBytes > steamIndexByteLimit {
		t.Fatal("compact cache did not converge", reads, stringsBytes)
	}
	// Installation status is still revalidated without a manifest parse
	if err := os.Remove(filepath.Join(root, "steamapps/common/Fixture-0001")); err != nil {
		t.Fatal(err)
	}
	changed, err := index.discover(context.Background(), root, "")
	if err != nil || len(changed) != 499 || reads != 500 || len(first) != 500 || first[0].Name != "Fixture-0001" {
		t.Fatal("installation validation or old snapshot changed", err)
	}
	if err := validateSteamInstallation(context.Background(), first[0]); err == nil {
		t.Fatal("removed installation launch accepted")
	}
	t.Logf("500 large manifests: metadata_estimated_bytes=%d inventory_estimated_bytes=%d retained_app_string_bytes=%d warm_allocs=%.0f warm_parse_count=%d", int64(index.files.Bytes()), index.inventoryBytes, stringsBytes, allocs, reads)
}

// Verify steam compact cache budget includes inventory and prunes roots
func TestSteamCompactCacheBudgetIncludesInventoryAndPrunesRoots(t *testing.T) {
	root := syntheticSteamLibrary(t, 10, 0, 1<<20)
	index := steamIndex{}
	games, err := index.discover(context.Background(), root, "")
	if err != nil || len(games) != 10 {
		t.Fatal(err, len(games))
	}
	if int64(index.files.Bytes())+index.inventoryBytes > steamIndexByteLimit || index.files.Len() >= 10 {
		t.Fatal("aggregate metadata/inventory budget ignored", int64(index.files.Bytes()), index.inventoryBytes, index.files.Len())
	}
	other := syntheticSteamLibrary(t, 1, 0, 0)
	if _, err = index.discover(context.Background(), other, ""); err != nil {
		t.Fatal(err)
	}
	if index.files.Len() != 1 || len(index.inventory) != 1 || int64(index.files.Bytes())+index.inventoryBytes > 4096 {
		t.Fatal("old root data retained", int64(index.files.Bytes()), index.inventoryBytes)
	}
}

// Verify steam rejects replaced and cancelled source before caching
func TestSteamRejectsReplacedAndCancelledSourceBeforeCaching(t *testing.T) {
	for _, cancelDuringRead := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelDuringRead), func(t *testing.T) {
			root := syntheticSteamLibrary(t, 1, 0, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			index := steamIndex{read: func(path string) (VDF, error) {
				data, err := readVDF(path)
				if err != nil {
					return data, err
				}
				if cancelDuringRead {
					cancel()
				} else {
					original, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					replaceSteamMetadata(t, path, original)
				}
				return data, nil
			}}
			if _, err := index.discover(ctx, root, ""); err == nil || index.files.Len() != 0 || int64(index.files.Bytes()) != 0 || index.inventory != nil {
				t.Fatal("invalidated parse cached", err)
			}
		})
	}
}

// Equal size and mtime deliberately leave file identity as the only signal
func replaceSteamMetadata(t *testing.T, path string, data []byte) {
	t.Helper()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != before.Size() {
		t.Fatal("replacement must preserve size")
	}
	temporary := path + ".replacement"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temporary, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("replacement did not preserve size and mtime", err)
	}
}

// Verify steam metadata identity survives path replacement
func TestSteamMetadataIdentitySurvivesPathReplacement(t *testing.T) {
	root := syntheticSteamLibrary(t, 1, 0, 0)
	path := filepath.Join(root, "steamapps", "appmanifest_1.acf")
	before, err := statSteamMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaceSteamMetadata(t, path, data)
	after, err := statSteamMetadata(path)
	if err != nil || os.SameFile(before, after) {
		t.Fatal("original identity followed replacement path", err)
	}
}

// Verify steam warm inventory revalidates equal size and mtime replacement
func TestSteamWarmInventoryRevalidatesEqualSizeAndMtimeReplacement(t *testing.T) {
	root := syntheticSteamLibrary(t, 1, 0, 0)
	reads := 0
	index := steamIndex{read: func(path string) (VDF, error) { reads++; return readVDF(path) }}
	first, err := index.discover(context.Background(), root, "")
	if err != nil || len(first) != 1 || first[0].Name != "Fixture-0001" {
		t.Fatal("initial inventory", err, first)
	}
	path := filepath.Join(root, "steamapps", "appmanifest_1.acf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), `"name" "Fixture-0001"`, `"name" "Renamed-0001"`, 1)
	if replaced == string(data) {
		t.Fatal("replacement did not change name")
	}
	replaceSteamMetadata(t, path, []byte(replaced))
	next, err := index.discover(context.Background(), root, "")
	if err != nil || len(next) != 1 || next[0].Name != "Renamed-0001" || reads != 2 || first[0].Name != "Fixture-0001" {
		t.Fatal("replacement reused stale inventory or mutated prior snapshot", err, next, reads)
	}
	warm, err := index.discover(context.Background(), root, "")
	if err != nil || reads != 2 || &warm[0] != &next[0] {
		t.Fatal("unchanged replacement did not reuse warm inventory", err, reads)
	}
}

// Verify steam metadata projection retains only valid playtimes and library paths
func TestSteamMetadataProjectionRetainsOnlyValidPlaytimesAndLibraryPaths(t *testing.T) {
	data, err := ParseVDF(`"UserLocalConfigStore" {"Software" {"Valve" {"Steam" {"apps" {"1" {"Playtime" "10" "Unused" "x"} "2" {"Playtime" "missing"} "bad-id" {"Playtime" "99"}}}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	compact := compactSteamMetadata("localconfig.vdf", data)
	if len(compact.playtime) != 1 || compact.playtime["1"] != 10 {
		t.Fatal(compact.playtime)
	}
	data, err = ParseVDF(`"libraryfolders" {"1" "/fixture/legacy" "2" {"path" "/fixture/modern" "apps" {"1" "lots of unused metadata"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	compact = compactSteamMetadata("libraryfolders.vdf", data)
	if len(compact.libraries) != 2 || compact.libraries[0] != "/fixture/legacy" || compact.libraries[1] != "/fixture/modern" {
		t.Fatal(compact.libraries)
	}
}
