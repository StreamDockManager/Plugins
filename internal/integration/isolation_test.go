package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// This is an allowlist of capabilities, not a list of observed binary sizes
// Similar plugins may legitimately have identically sized executables
var featureCapabilities = map[string][]string{
	"obs-studio":         {"connectors"},
	"home-assistant":     {"connectors"},
	"random-meme":        {"connectors"},
	"vlc":                {"connectors"},
	"tailscale":          {"connectors", "connectors/command", "native/command"},
	"mqtt":               {"connectors"},
	"make":               {"connectors"},
	"osrs-hiscores":      {"connectors", "hiscores"},
	"runescape-hiscores": {"connectors", "hiscores"},
	"retroachievements":  {"connectors"},

	"battery-power":       {"native/battery", "native/command"},
	"http-webhooks":       {"httpclient", "pollingdisplay"},
	"microphone-controls": {"microphone"},
	"network-vpn":         {"native/command", "native/vpn"},
	"screenshot-tools":    {"native/capture", "native/command"},
	"service-monitor":     {"httpclient", "native/command", "pollingdisplay"},
	"spotify":             {"native/command", "native/spotify", "microphone"},
}

// repositoryRoot locates the repository root for fixture inputs
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// TestEveryPluginCompilesOnlyItsDeclaredCapabilities verifies every plugin compiles only its declared capabilities
func TestEveryPluginCompilesOnlyItsDeclaredCapabilities(t *testing.T) {
	root := repositoryRoot(t)
	for _, target := range [][2]string{{"linux", "amd64"}, {"windows", "amd64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		t.Run(target[0]+"/"+target[1], func(t *testing.T) {
			cmd := exec.Command("go", "list", "-json", "./cmd/...")
			cmd.Dir = root
			// Preserve an explicitly selected development SDK workspace; dependency
			// closure assertions are identical for local and versioned SDK packages
			cmd.Env = append(os.Environ(), "GOOS="+target[0], "GOARCH="+target[1], "CGO_ENABLED=0", "GOFLAGS=")
			data, err := cmd.Output()
			if err != nil {
				if x, ok := err.(*exec.ExitError); ok {
					t.Fatalf("dependency inspection: %s", x.Stderr)
				}
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			count := 0
			const prefix = "github.com/StreamDockManager/Plugins/"
			for {
				var p struct {
					ImportPath string
					Deps       []string
				}
				if err := decoder.Decode(&p); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				slug := strings.TrimPrefix(p.ImportPath, prefix+"cmd/")
				if slug == "weather-geocoding-proxy" {
					// The optional geocoding broker is a helper, not an installable
					// plugin. Keep its dependency closure isolated as well
					if !slices.Contains(p.Deps, prefix+"internal/nominatimbroker") {
						t.Error("geocoding helper is missing its broker implementation")
					}
					for _, dep := range p.Deps {
						if strings.HasPrefix(dep, prefix+"internal/") && dep != prefix+"internal/nominatimbroker" {
							t.Errorf("geocoding helper imports unrelated capability %s", dep)
						}
					}
					continue
				}
				count++
				// coder/websocket owns host transport; HTTP/TLS are now core dependencies
				// External service clients remain opt-in
				if slug == "counter-tally" || slug == "timers-pomodoro" || slug == "randomizer-dice" {
					for _, forbidden := range []string{"github.com/StreamDockManager/Plugin-SDK/Go/httpclient", "github.com/StreamDockManager/Plugin-SDK/Go/connectedws", "github.com/gorilla/websocket"} {
						if slices.Contains(p.Deps, forbidden) {
							t.Errorf("%s imports unused networking dependency %s", slug, forbidden)
						}
					}
				}
				own := prefix + "internal/plugins/" + slug
				if !slices.Contains(p.Deps, own) {
					t.Errorf("%s has no explicit feature implementation", slug)
				}
				allowed := append([]string{"engine", "settings", "plugins/" + slug}, featureCapabilities[slug]...)
				for _, dep := range p.Deps {
					if dep == "image/gif" && slug != "spotify" && slug != "random-meme" || dep == "image/jpeg" && slug != "spotify" && slug != "screenshot-tools" && slug != "random-meme" {
						t.Errorf("%s imports unrelated artwork decoder %s", slug, dep)
					}
					if dep == "image/png" && slug != "spotify" && slug != "screenshot-tools" && slug != "random-meme" {
						t.Errorf("%s registers an unused PNG decoder", slug)
					}
					if strings.HasPrefix(dep, prefix+"internal/") && !slices.Contains(allowed, strings.TrimPrefix(dep, prefix+"internal/")) {
						t.Errorf("%s imports unrelated capability %s", slug, dep)
					}
					for module, owner := range map[string]string{"github.com/showwin/speedtest-go/": "internet-speed-test", "github.com/shirou/gopsutil/": "system-monitor"} {
						if strings.HasPrefix(dep, module) && slug != owner {
							t.Errorf("%s imports %s, owned by %s", slug, dep, owner)
						}
					}
					if strings.HasPrefix(dep, "github.com/ebitengine/purego") && slug != "microphone-controls" && slug != "system-monitor" && slug != "spotify" {
						t.Errorf("%s imports an unused native audio backend", slug)
					}
				}
			}
			if count != 24 {
				t.Errorf("inspected %d plugins, want 24", count)
			}
		})
	}
}

// Opt in for final verification; ordinary unit tests do not rebuild executables
func TestRepresentativeLinkedSymbolsExcludeOtherFeatures(t *testing.T) {
	if os.Getenv("STREAM_DOCK_MANAGER_LINKAGE_CHECK") != "1" {
		t.Skip("set STREAM_DOCK_MANAGER_LINKAGE_CHECK=1")
	}
	root := repositoryRoot(t)
	for _, slug := range []string{"counter-tally", "spotify", "http-webhooks", "system-monitor", "internet-speed-test"} {
		t.Run(slug, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plugin")
			cmd := exec.Command("go", "build", "-trimpath", "-o", path, "./cmd/"+slug)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, data)
			}
			data, err := exec.Command("go", "tool", "nm", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			for line := range strings.SplitSeq(string(data), "\n") {
				const marker = "StreamDockManager-Plugins/internal/plugins/"
				if _, rest, ok := strings.Cut(line, marker); ok && !strings.HasPrefix(rest, slug+".") {
					t.Errorf("unrelated feature symbol: %s", line)
				}
			}
			if slug == "counter-tally" {
				for _, marker := range []string{"speedtest-go/", "gopsutil/", "internal/microphone.", "internal/native/spotify.", ".thumbnail", "/httpclient.", "/connectedws.", "github.com/gorilla/websocket."} {
					if bytes.Contains(data, []byte(marker)) {
						t.Errorf("counter links %s", marker)
					}
				}
			}
		})
	}
}
