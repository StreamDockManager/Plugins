package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/shirou/gopsutil/v4/process"
)

type workloadMeasurement struct {
	Plugin         string  `json:"plugin"`
	Bindings       int     `json:"bindings"`
	Iterations     int     `json:"iterations"`
	WallNS         int64   `json:"wall_ns"`
	UserCPU        float64 `json:"user_cpu_seconds"`
	SystemCPU      float64 `json:"system_cpu_seconds"`
	AllocatedBytes uint64  `json:"allocated_bytes"`
	Allocations    uint64  `json:"allocations"`
	RetainedHeap   uint64  `json:"retained_heap_bytes"`
	RSS            uint64  `json:"rss_bytes"`
	Goroutines     int     `json:"goroutines"`
	StateFiles     int     `json:"state_files_written"`
}

// Explicit, repeatable process measurements. Run alone, without -race, for resource data
// These isolate the shared display path; action/OS behavior belongs to the fixture suites
func TestOfficialPluginResourceMeasurements(t *testing.T) {
	output := os.Getenv("STREAM_DOCK_MANAGER_PERFORMANCE_REPORT")
	if output == "" {
		t.Skip("set STREAM_DOCK_MANAGER_PERFORMANCE_REPORT to save resource measurements")
	}
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	var report []workloadMeasurement
	for _, definition := range Definitions {
		for _, count := range []int{0, 1, 32} {
			path := filepath.Join(t.TempDir(), "state.json")
			e := New(definition.Slug, path)
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprint(i)
				e.Bindings[ids[i]] = binding{Action: definition.Actions[0]}
				seedMeasuredDisplay(e, ids[i], displayStatus{Text: "Ready"})
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			cpuBefore, err := proc.Times()
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			const iterations = 1000
			for range iterations {
				for _, id := range ids {
					if err := e.SendDisplay(context.Background(), nil, id, displayStatus{Text: "Ready"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			elapsed := time.Since(started)
			cpuAfter, err := proc.Times()
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			allocated, allocations := after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
			runtime.GC()
			runtime.ReadMemStats(&after)
			memory, err := proc.MemoryInfo()
			if err != nil {
				t.Fatal(err)
			}
			record := workloadMeasurement{Plugin: definition.Slug, Bindings: count, Iterations: iterations, WallNS: elapsed.Nanoseconds(), UserCPU: cpuAfter.User - cpuBefore.User, SystemCPU: cpuAfter.System - cpuBefore.System, AllocatedBytes: allocated, Allocations: allocations, RetainedHeap: after.HeapAlloc, RSS: memory.RSS, Goroutines: runtime.NumGoroutine()}
			runtime.KeepAlive(e)
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err == nil {
				record.StateFiles = 1
			}
			report = append(report, record)
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// seedMeasuredDisplay seeds a measured display fixture
func seedMeasuredDisplay(e *engine.Engine, id string, status displayStatus) {
	seedMeasuredView(e, id, e.DisplayView(id, status))
	e.LastStatus[id] = status
}

// seedMeasuredView seeds a measured action view fixture
func seedMeasuredView(e *engine.Engine, id string, view pluginDisplay) { e.Last[id] = view }

type binding = engine.Binding
type displayStatus = engine.DisplayStatus
type pluginDisplay = engine.Display
