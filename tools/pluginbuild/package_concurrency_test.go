package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// TestPackageWorkersBoundedAndErrorsDeterministic verifies package workers bounded and errors deterministic
func TestPackageWorkersBoundedAndErrorsDeterministic(t *testing.T) {
	builder := testRunner(t)
	builder.jobs = 2
	ds := []official.Definition{{Slug: "first"}, {Slug: "second"}, {Slug: "third"}}
	var active, peak atomic.Int32
	err := builder.parallelPackages(ds, func(d official.Definition) error {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		if d.Slug == "first" {
			time.Sleep(30 * time.Millisecond)
		}
		return errors.New(d.Slug + " failure")
	})
	if peak.Load() != 2 {
		t.Fatalf("wanted two workers, got %d", peak.Load())
	}
	if err == nil || !strings.HasPrefix(err.Error(), "first:") {
		t.Fatalf("completion-order error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	builder.ctx = ctx
	if err := builder.parallelPackages(ds, func(official.Definition) error { t.Error("cancelled work started"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// TestBuildOutputWriterSerializesSharedBuffers verifies build output writer serializes shared buffers
func TestBuildOutputWriterSerializesSharedBuffers(t *testing.T) {
	var output bytes.Buffer
	writer := lockedBuildWriter{mu: &sync.Mutex{}, writer: &output}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				fmt.Fprintln(writer, "whole-line")
			}
		})
	}
	workers.Wait()
	if strings.Count(output.String(), "whole-line\n") != 800 {
		t.Fatal("interleaved output")
	}
}

// BenchmarkPackageScheduling measures package scheduling
func BenchmarkPackageScheduling(b *testing.B) {
	for _, jobs := range []int{1, 4} {
		b.Run(fmt.Sprint(jobs), func(b *testing.B) {
			builder := &buildRunner{buildConfig: buildConfig{jobs: jobs}, ctx: context.Background()}
			ds := make([]official.Definition, 8)
			for i := range ds {
				ds[i].Slug = fmt.Sprint(i)
			}
			for b.Loop() {
				if err := builder.parallelPackages(ds, func(official.Definition) error { time.Sleep(time.Millisecond); return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestParallelPackagingCatalogMatchesSerial verifies parallel packaging catalog matches serial
func TestParallelPackagingCatalogMatchesSerial(t *testing.T) {
	builder := packageFixture(t)
	builder.jobs = 1
	if err := builder.packWithNotices("linux", []byte("licenses")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(builder.root, "dist", "catalog-linux.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	builder.jobs = 4
	builder.force = true
	if err := builder.packWithNotices("linux", []byte("licenses")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("parallel completion changed catalog ordering or content")
	}
}
