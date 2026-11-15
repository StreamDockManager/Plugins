// A single shared instance protects the public Nominatim upstream's global budget
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/StreamDockManager/Plugins/internal/nominatimbroker"
	"log"
	"net/http"
	"os"
	"time"
)

// main runs the weather geocoding proxy command
func main() {
	config := flag.String("config", "nominatim.json", "Operator-owned JSON configuration (reloaded for each search)")
	listen := flag.String("listen", "127.0.0.1:8095", "Listen address; publish behind an HTTPS reverse proxy")
	flag.Parse()
	broker := &nominatimbroker.Broker{Config: func() (nominatimbroker.Config, error) {
		var cfg nominatimbroker.Config
		data, err := os.ReadFile(*config)
		if err != nil {
			return cfg, err
		}
		err = json.Unmarshal(data, &cfg)
		return cfg, err
	}}
	go broker.RunCleanup(context.Background())
	server := &http.Server{Addr: *listen, Handler: http.MaxBytesHandler(broker, 2048), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	// Do not log requests: address queries can contain personal information
	log.Fatal(server.ListenAndServe())
}
