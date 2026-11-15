// Local-only acceptance host for the packaged inspector and a compiled Weather executable
// It uses a private TLS forecast fixture; no public weather or geocoding service is contacted
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// main runs the weatheracceptance command
func main() {
	binary := flag.String("binary", "", "compiled Weather executable")
	flag.Parse()
	if *binary == "" {
		panic("-binary required")
	}
	var mu sync.Mutex
	var runtime *websocket.Conn
	inspectors := map[*websocket.Conn]string{}
	settings := map[string]map[string]any{}
	titles := map[string]string{}
	var calls []string
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unit := r.URL.Query().Get("temperature_unit")
		mu.Lock()
		calls = append(calls, unit)
		mu.Unlock()
		temp, low, high := 12.5, 8., 15.
		if unit == "fahrenheit" {
			temp, low, high = 54.5, 46.4, 59.
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"current": map[string]any{"temperature_2m": temp, "weather_code": 2}, "daily": map[string]any{"temperature_2m_min": []float64{low}, "temperature_2m_max": []float64{high}, "sunrise": []string{"2026-10-27T07:30"}, "sunset": []string{"2026-10-27T17:45"}}})
	}))
	defer fixture.Close()
	dir, err := os.MkdirTemp("", "weather-acceptance-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	cert := filepath.Join(dir, "fixture.pem")
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate().Raw}), 0600)
	base := func() map[string]any {
		return map[string]any{"latitude": 51.5, "longitude": -0.12, "units": "celsius", "endpoint": fixture.URL, "legacy_unknown": map[string]any{"retained": true}}
	}
	settings["binding"] = base()
	settings["defaults"] = base()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			var reg map[string]any
			if wsjson.Read(context.Background(), c, &reg) != nil {
				return
			}
			scope, _ := reg["uuid"].(string)
			mu.Lock()
			if reg["event"] == "registerPlugin" {
				runtime = c
				wsjson.Write(context.Background(), c, map[string]any{"event": "didReceiveGlobalSettings", "payload": map[string]any{"settings": map[string]any{}}})
			} else {
				inspectors[c] = scope
			}
			mu.Unlock()
			defer func() { mu.Lock(); delete(inspectors, c); mu.Unlock() }()
			for {
				var ev map[string]any
				if wsjson.Read(context.Background(), c, &ev) != nil {
					return
				}
				event, _ := ev["event"].(string)
				ctx, _ := ev["context"].(string)
				mu.Lock()
				switch event {
				case "setSettings":
					if payload, ok := ev["payload"].(map[string]any); ok {
						settings[scope] = payload
						wsjson.Write(context.Background(), c, map[string]any{"event": "settingsDraftSaved", "context": scope})
					}
				case "sendToPlugin":
					if runtime != nil {
						wsjson.Write(context.Background(), runtime, ev)
					}
				case "sendToPropertyInspector":
					for pi, owner := range inspectors {
						if owner == ctx {
							wsjson.Write(context.Background(), pi, ev)
						}
					}
				case "setTitle":
					if payload, ok := ev["payload"].(map[string]any); ok {
						titles[ctx], _ = payload["title"].(string)
					}
				}
				mu.Unlock()
			}
		}
		switch r.URL.Path {
		case "/state":
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"settings": settings, "titles": titles, "calls": calls})
			return
		case "/apply":
			mu.Lock()
			defer mu.Unlock()
			scope := r.URL.Query().Get("scope")
			if scope == "new-binding" {
				copy := map[string]any{}
				maps.Copy(copy, settings["defaults"])
				settings[scope] = copy
			}
			if runtime == nil {
				http.Error(w, "runtime starting", http.StatusServiceUnavailable)
				return
			}
			event := "didReceiveSettings"
			if _, ok := titles[scope]; !ok {
				event = "willAppear"
			}
			delete(titles, scope)
			wsjson.Write(context.Background(), runtime, map[string]any{"event": event, "action": "app.streamdockmanager.plugins.weather.conditions", "context": scope, "payload": map[string]any{"settings": settings[scope]}, "streamDockManager": map[string]any{"width": 112, "height": 112}})
			w.WriteHeader(204)
			return
		case "/weather/inspector.html":
			scope := r.URL.Query().Get("scope")
			if scope == "" {
				scope = "binding"
			}
			mu.Lock()
			value := settings[scope]
			mu.Unlock()
			data, err := os.ReadFile("dist/ui/weather/inspector.html")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			info, _ := json.Marshal(map[string]any{"streamDockManager": map[string]any{"enabled": true}})
			details, _ := json.Marshal(map[string]any{"action": "app.streamdockmanager.plugins.weather.conditions", "context": scope, "payload": map[string]any{"settings": value, "scope": "action"}})
			args, _ := json.Marshal([]string{fmt.Sprint(port), scope, "registerPropertyInspector", string(info), string(details)})
			w.Header().Set("Content-Type", "text/html")
			w.Write(data)
			fmt.Fprintf(w, "<script>addEventListener('load',()=>connectElgatoStreamDeckSocket(...%s))</script>", args)
			return
		}
		http.StripPrefix("/weather/", http.FileServer(http.Dir("dist/ui/weather"))).ServeHTTP(w, r)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, *binary, "-port", fmt.Sprint(port), "-pluginUUID", "runtime", "-registerEvent", "registerPlugin", "-info", "{}")
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+cert, "STREAM_DOCK_MANAGER_PLUGIN_STATE_DIR="+dir)
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		panic(err)
	}
	defer func() { cancel(); cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		ready := runtime != nil
		mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			panic("runtime did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", port)})
	bufio.NewReader(os.Stdin).ReadString('\n')
}
