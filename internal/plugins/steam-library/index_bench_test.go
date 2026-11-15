package steamlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Fixed synthetic library, independent of the user's Steam installation
func BenchmarkSteamLibraryNavigation(b *testing.B) {
	root := b.TempDir()
	apps := filepath.Join(root, "steamapps")
	for i := 1; i <= 500; i++ {
		name := fmt.Sprintf("Fixture-%04d", i)
		if err := os.MkdirAll(filepath.Join(apps, "common", name), 0700); err != nil {
			b.Fatal(err)
		}
		data := fmt.Sprintf(`"AppState" { "appid" "%d" "name" "%s" "StateFlags" "4" "installdir" "%s" }`, i, name, name)
		if err := os.WriteFile(filepath.Join(apps, fmt.Sprintf("appmanifest_%d.acf", i)), []byte(data), 0600); err != nil {
			b.Fatal(err)
		}
	}
	for _, mode := range []string{"cold", "warm"} {
		b.Run(mode, func(b *testing.B) {
			e := New(filepath.Join(b.TempDir(), "state.json"))
			defer func() { e.Close() }()
			browse := func() {
				e.startSteam(context.Background(), "fixture", "next", map[string]any{"steam_path": root})
				result := <-e.Completed
				if result.Err != nil {
					b.Fatal(result.Err)
				}
				e.CancelJob("fixture")
			}
			if mode == "warm" {
				browse()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if mode == "cold" {
					e.Close()
					e = New(filepath.Join(root, "state.json"))
				}
				browse()
			}
		})
	}
}
