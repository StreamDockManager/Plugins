package steamlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verify steam index reuses parses and invalidates files
func TestSteamIndexReusesParsesAndInvalidatesFiles(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "steamapps")
	os.MkdirAll(filepath.Join(apps, "common", "Fixture"), 0700)
	manifest := filepath.Join(apps, "appmanifest_42.acf")
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(manifest, fmt.Appendf(nil, `"AppState" { "appid" "42" "name" "%s" "StateFlags" "4" "installdir" "Fixture" }`, name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Fixture")
	reads := 0
	index := steamIndex{read: func(path string) (VDF, error) { reads++; return readVDF(path) }}
	for range 5 {
		games, err := index.discover(context.Background(), root, "")
		if err != nil || len(games) != 1 {
			t.Fatal(games, err)
		}
	}
	if reads != 1 {
		t.Fatal("warm navigation parsed unchanged metadata", reads)
	}
	write("Changed fixture")
	games, err := index.discover(context.Background(), root, "")
	if err != nil || games[0].Name != "Changed fixture" || reads != 2 {
		t.Fatal(games, err, reads)
	}
	if err = validateSteamInstallation(context.Background(), games[0]); err != nil {
		t.Fatal(err)
	}
	os.Remove(manifest)
	if err = validateSteamInstallation(context.Background(), games[0]); err == nil {
		t.Fatal("stale launch accepted")
	}
	if _, err = index.discover(context.Background(), root, ""); err == nil || index.files.Len() != 0 {
		t.Fatal("removed manifest retained", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = index.discover(ctx, root, ""); err != context.Canceled {
		t.Fatal("cancelled scan accepted", err)
	}
}

// Verify steam index waiting scan can cancel
func TestSteamIndexWaitingScanCanCancel(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "steamapps")
	os.MkdirAll(filepath.Join(apps, "common", "Fixture"), 0700)
	os.WriteFile(filepath.Join(apps, "appmanifest_1.acf"), []byte(`"AppState" {"appid" "1" "name" "Fixture" "StateFlags" "4" "installdir" "Fixture"}`), 0600)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	index := &steamIndex{read: func(path string) (VDF, error) { close(entered); <-release; return readVDF(path) }}
	go func() { defer close(finished); index.discover(context.Background(), root, "") }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := index.discover(ctx, root, ""); done <- err }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled navigation waited for unrelated parse")
	}
	close(release)
	<-finished
}

// Verify steam index tracks library account and path changes
func TestSteamIndexTracksLibraryAccountAndPathChanges(t *testing.T) {
	makeLibrary := func(id, name string) string {
		root := t.TempDir()
		apps := filepath.Join(root, "steamapps")
		os.MkdirAll(filepath.Join(apps, "common", name), 0700)
		data := fmt.Sprintf(`"AppState" {"appid" "%s" "name" "%s" "StateFlags" "4" "installdir" "%s"}`, id, name, name)
		os.WriteFile(filepath.Join(apps, "appmanifest_"+id+".acf"), []byte(data), 0600)
		return root
	}
	root := makeLibrary("1", "First")
	other := makeLibrary("2", "Second")
	for _, account := range []string{"10", "20"} {
		folder := filepath.Join(root, "userdata", account, "config")
		os.MkdirAll(folder, 0700)
		data := fmt.Sprintf(`"UserLocalConfigStore" {"Software" {"Valve" {"Steam" {"apps" {"1" {"Playtime" "%s"}}}}}}`, account)
		os.WriteFile(filepath.Join(folder, "localconfig.vdf"), []byte(data), 0600)
	}
	index := &steamIndex{}
	for _, account := range []string{"10", "20"} {
		games, err := index.discover(context.Background(), root, account)
		if err != nil || len(games) != 1 || games[0].PlaytimeMinutes == nil || fmt.Sprint(*games[0].PlaytimeMinutes) != account {
			t.Fatal("account metadata was stale", games, err)
		}
	}
	config := filepath.Join(root, "steamapps", "libraryfolders.vdf")
	os.WriteFile(config, fmt.Appendf(nil, `"libraryfolders" {"1" {"path" "%s"}}`, other), 0600)
	games, err := index.discover(context.Background(), root, "")
	if err != nil || len(games) != 2 {
		t.Fatal("new library not discovered", games, err)
	}
	os.Remove(config)
	games, err = index.discover(context.Background(), root, "")
	if err != nil || len(games) != 1 || games[0].ID != "1" {
		t.Fatal("removed library retained", games, err)
	}
	games, err = index.discover(context.Background(), other, "")
	if err != nil || len(games) != 1 || games[0].ID != "2" {
		t.Fatal("changed Steam root retained old index", games, err)
	}
}
