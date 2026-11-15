// displaystress is a bounded, side-effect-free display engine/SDK fixture runner
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

// main runs the displaystress command
func main() {
	cycles := flag.Int("cycles", 120, "bounded seven-step scenario cycles (1..1200)")
	surfaces := flag.Int("surfaces", 10, "synthetic key/touch surfaces (1..14)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, err := displaybench.RunScenarios(ctx, displaybench.ScenarioOptions{Cycles: *cycles, Surfaces: *surfaces}, sourceRevision, sourceDirty)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
