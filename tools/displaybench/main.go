// displaybench is a bounded, side-effect-free display engine/SDK fixture runner
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/displaybench"
	"os"
	"os/signal"
	"syscall"
)

// Set by reproducible builds with -ldflags '-X main.sourceRevision=<commit>'
var sourceRevision = "unknown"
var sourceDirty = "unknown"

// main runs the displaybench command
func main() {
	frames := flag.Int("frames", 120, "synthetic frame revisions (1..1200)")
	surfaces := flag.Int("surfaces", 10, "synthetic key/touch surfaces (1..14)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, err := displaybench.Run(ctx, displaybench.Options{Frames: *frames, Surfaces: *surfaces}, sourceRevision, sourceDirty)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
