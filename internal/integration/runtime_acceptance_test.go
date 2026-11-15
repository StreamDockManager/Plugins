//go:build darwin

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"github.com/StreamDockManager/Plugins/internal/testhost"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/microphone"
	counter "github.com/StreamDockManager/Plugins/internal/plugins/counter-tally"
	microphonefeature "github.com/StreamDockManager/Plugins/internal/plugins/microphone-controls"
	system "github.com/StreamDockManager/Plugins/internal/plugins/system-monitor"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// This harness constructs explicit feature factories and instruments the production
// owner loop without replacing scheduling, jobs, rendering, or persistence
type acceptanceHandler struct {
	*Engine
	caseName                       string
	started                        time.Time
	polls, peakJobs, handled       int
	probeCalls, active, peakActive atomic.Int64
	mu                             sync.Mutex
	probeTimes                     []float64
	lastFile                       os.FileInfo
	writes                         []float64
	firstMutation                  time.Time
	mutationWrites                 []int
	mutations                      int
	writeMutations                 []int
}

// observe records the runtime observation for later assertions
func (h *acceptanceHandler) observe() {
	h.peakJobs = max(h.peakJobs, len(h.Jobs))
	if h.caseName != "persistence" {
		return
	}
	info, err := os.Stat(h.Path)
	if err == nil && (h.lastFile == nil || !os.SameFile(h.lastFile, info)) {
		h.lastFile = info
		h.writes = append(h.writes, time.Since(h.firstMutation).Seconds())
		h.writeMutations = append(h.writeMutations, h.mutations)
	}
}

// Handle handles an event in the acceptance fixture
func (h *acceptanceHandler) Handle(ctx context.Context, c *sdk.Client, event sdk.Event) error {
	if event.Event == "keyDown" && h.firstMutation.IsZero() {
		h.firstMutation = time.Now()
	}
	if event.Event == "keyDown" {
		h.mutations++
	}
	if event.Event == "acceptanceCheckpoint" {
		h.observe()
		h.mutationWrites = append(h.mutationWrites, len(h.writes))
	} else {
		if err := h.Engine.Handle(ctx, c, event); err != nil {
			return err
		}
		h.handled++
		h.observe()
	}
	return c.Send(ctx, sdk.Message("acceptanceAck", event.Context, map[string]any{
		"event": event.Event, "generation": event.Data().Settings["fixture_generation"],
		"sent": event.Data().Settings["fixture_sent"],
	}))
}

// Poll advances the acceptance fixture on its polling deadline
func (h *acceptanceHandler) Poll(ctx context.Context, c *sdk.Client) error {
	h.polls++
	err := h.Engine.Poll(ctx, c)
	h.observe()
	return err
}

// Close releases the owned resources and pending work
func (h *acceptanceHandler) Close() error {
	err := h.Engine.Close()
	h.observe()
	return err
}

type acceptanceMicrophone struct{ handler *acceptanceHandler }

// Devices lists the available microphone devices
func (a acceptanceMicrophone) Devices(context.Context) ([]microphone.Device, error) {
	return nil, fmt.Errorf("discovery is outside the controlled workload")
}

// Default returns the default input device
func (a acceptanceMicrophone) Default(context.Context) (string, error) { return "Fixture/0", nil }

// SetMute applies the requested microphone mute state
func (a acceptanceMicrophone) SetMute(context.Context, string, bool) error {
	return fmt.Errorf("mute changes are outside the read-only workload")
}

// Read reads the requested data through the owned adapter
func (a acceptanceMicrophone) Read(ctx context.Context, selected string) (microphone.Device, error) {
	h := a.handler
	h.probeCalls.Add(1)
	active := h.active.Add(1)
	defer h.active.Add(-1)
	for old := h.peakActive.Load(); active > old && !h.peakActive.CompareAndSwap(old, active); old = h.peakActive.Load() {
	}
	h.mu.Lock()
	h.probeTimes = append(h.probeTimes, time.Since(h.started).Seconds())
	h.mu.Unlock()
	delay := 50 * time.Millisecond
	if h.caseName == "failing_unique" || h.caseName == "rapid_reconfiguration" {
		delay = 150 * time.Millisecond
	}
	// Deliberately complete old reads after reconfiguration: the production
	// cancellation/generation path must reject their late results
	time.Sleep(delay)
	if h.caseName == "failing_unique" {
		return microphone.Device{}, fmt.Errorf("controlled unavailable source")
	}
	parts := strings.Split(selected, "/")
	if len(parts) < 2 {
		return microphone.Device{}, fmt.Errorf("invalid controlled microphone identity")
	}
	return microphone.Device{ID: selected, Name: "Fixture/" + parts[1], Gain: .42, Mode: "fixture input"}, nil
}

// acceptanceCPU samples process CPU for the acceptance check
func acceptanceCPU(value syscall.Rusage) float64 {
	return float64(value.Utime.Sec+value.Stime.Sec) + float64(value.Utime.Usec+value.Stime.Usec)/1e6
}

// acceptanceChild runs the isolated child-process fixture
func acceptanceChild(t *testing.T) {
	name := os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_CASE")
	slug := "microphone-controls"
	if name == "telemetry_expiry" {
		slug = "system-monitor"
	}
	if name == "persistence" {
		slug = "counter-tally"
	}
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	h := &acceptanceHandler{caseName: name, started: started}
	path := os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_STATE")
	switch slug {
	case "microphone-controls":
		h.Engine = fixtureengine.New(microphonefeature.Definition, path, microphonefeature.WithMicrophoneAdapter(acceptanceMicrophone{h}))
	case "counter-tally":
		h.Engine = fixtureengine.New(counter.Definition, path, counter.Factory)
	case "system-monitor":
		h.Engine = fixtureengine.New(system.Definition, path, system.Factory)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := testhost.Connect(ctx, os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_HOST"), "local-acceptance", h); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for h.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.active.Load() != 0 {
		t.Fatal("probe did not finish after shutdown")
	}
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	probeTimes := append([]float64(nil), h.probeTimes...)
	h.mu.Unlock()
	record := map[string]any{"case": name, "wall_seconds": time.Since(started).Seconds(), "cpu_seconds": acceptanceCPU(after) - acceptanceCPU(before),
		"peak_rss_bytes": after.Maxrss, "polls": h.polls, "handled_inputs": h.handled, "peak_live_jobs": h.peakJobs,
		"probe_calls": h.probeCalls.Load(), "peak_running_probes": h.peakActive.Load(), "probe_started_seconds": probeTimes,
		"state_write_seconds": h.writes, "checkpoint_write_counts": h.mutationWrites, "remaining_active_probes": h.active.Load(),
		"mutation_inputs_at_write": h.writeMutations,
		"remaining_jobs":           len(h.Jobs), "go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH}
	if name == "persistence" {
		data, err := os.ReadFile(h.Path)
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		record["final_state"] = saved
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_CHILD_REPORT"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// Opt-in only: no desktop/device commands or public network. The child process
// runs production Engine/SDK code; the parent owns loopback input and observation
func TestRuntimeWorkloadAcceptance(t *testing.T) {
	if os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_CHILD") == "1" {
		acceptanceChild(t)
		return
	}
	destination := os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_REPORT")
	if destination == "" {
		t.Skip("set STREAM_DOCK_MANAGER_ACCEPTANCE_REPORT and STREAM_DOCK_MANAGER_ACCEPTANCE_CASE")
	}
	name := os.Getenv("STREAM_DOCK_MANAGER_ACCEPTANCE_CASE")
	if !map[string]bool{"idle": true, "visible_shared": true, "failing_unique": true, "rapid_reconfiguration": true, "telemetry_expiry": true, "persistence": true}[name] {
		t.Fatal("unknown acceptance case")
	}
	directory := t.TempDir()
	childReport, state := filepath.Join(directory, "child.json"), filepath.Join(directory, "state.json")
	var mu sync.Mutex
	latencies := []float64{}
	titles := []map[string]any{}
	expected := map[string]float64{}
	pending := map[string]int{}
	stale, outputs, acks := 0, 0, 0
	matchingFeedback, pendingFeedback, unavailableFeedback := 0, 0, 0
	ready := make(chan *websocket.Conn, 1)
	readerDone := make(chan struct{})
	started := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(1 << 20)
		defer close(readerDone)
		var registration sdk.Event
		if err := wsjson.Read(context.Background(), conn, &registration); err != nil {
			t.Error(err)
			return
		}
		if registration.Event != "registerPlugin" || registration.UUID != "local-acceptance" {
			t.Error("invalid registration")
			return
		}
		ready <- conn
		for {
			var event sdk.Event
			if err := wsjson.Read(context.Background(), conn, &event); err != nil {
				return
			}
			var data map[string]any
			if err := json.Unmarshal(event.Payload, &data); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			outputs++
			if event.Event == "acceptanceAck" {
				acks++
				if data["event"] == "willAppear" || data["event"] == "didReceiveSettings" || data["event"] == "willDisappear" {
					pending[event.Context]--
				}
				if sent, ok := data["sent"].(float64); ok {
					latencies = append(latencies, float64(time.Now().UnixNano())/1e6-sent)
				}
				if data["event"] == "willDisappear" {
					delete(expected, event.Context)
				}
				if data["event"] == "willAppear" || data["event"] == "didReceiveSettings" {
					expected[event.Context], _ = data["generation"].(float64)
				}
			}
			if event.Event == "setTitle" {
				title, _ := data["title"].(string)
				if strings.HasPrefix(title, "Fixture/") {
					var generation float64
					_, _ = fmt.Sscanf(title, "Fixture/%f", &generation)
					if pending[event.Context] > 0 {
						pendingFeedback++
					} else if want, ok := expected[event.Context]; !ok || generation != want {
						stale++
					} else {
						matchingFeedback++
					}
				}
				if strings.Contains(title, "Unavailable") {
					unavailableFeedback++
				}
				if name == "telemetry_expiry" {
					titles = append(titles, map[string]any{"seconds": time.Since(started).Seconds(), "context": event.Context, "title": title})
				}
			}
			if event.Event == "logMessage" {
				t.Errorf("production handler reported an error: %s", event.Payload)
			}
			mu.Unlock()
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeWorkloadAcceptance$", "-test.count=1")
	command.Env = append(os.Environ(), "STREAM_DOCK_MANAGER_ACCEPTANCE_CHILD=1", "STREAM_DOCK_MANAGER_ACCEPTANCE_HOST=ws"+strings.TrimPrefix(server.URL, "http"), "STREAM_DOCK_MANAGER_ACCEPTANCE_STATE="+state, "STREAM_DOCK_MANAGER_ACCEPTANCE_CHILD_REPORT="+childReport)
	var childOutput bytes.Buffer
	command.Stdout, command.Stderr = &childOutput, &childOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	var conn *websocket.Conn
	select {
	case conn = <-ready:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("child did not register: %s", childOutput.String())
	}
	send := func(event sdk.Event) {
		t.Helper()
		if event.Event == "willAppear" || event.Event == "didReceiveSettings" || event.Event == "willDisappear" {
			mu.Lock()
			pending[event.Context]++
			mu.Unlock()
		}
		if err := wsjson.Write(context.Background(), conn, event); err != nil {
			t.Fatal(err)
		}
	}
	input := func(kind, id string, generation int, unique bool) {
		t.Helper()
		settings := map[string]any{"fixture_generation": generation, "device": fmt.Sprintf("Fixture/%d", generation)}
		if unique {
			settings["device"] = fmt.Sprintf("Fixture/%d/%s", generation, id)
		}
		payload, _ := json.Marshal(map[string]any{"settings": settings})
		send(sdk.Event{Event: kind, Context: id, Action: "microphone.toggle", Payload: payload})
	}
	ping := func() {
		payload, _ := json.Marshal(map[string]any{"settings": map[string]any{"fixture_sent": float64(time.Now().UnixNano()) / 1e6}})
		send(sdk.Event{Event: "propertyInspectorDidAppear", Payload: payload})
	}
	wait := func(duration time.Duration) {
		end := time.Now().Add(duration)
		for time.Now().Before(end) {
			ping()
			time.Sleep(min(100*time.Millisecond, time.Until(end)))
		}
	}
	switch name {
	case "telemetry_expiry":
		snapshot := func(value float64) {
			payload, _ := json.Marshal(sdk.TelemetrySnapshot{CPU: sdk.Measurement{Value: &value, ObservedAt: uint64(time.Now().UnixMilli()), MaxAgeMS: 2500}})
			send(sdk.Event{Event: "didReceiveTelemetry", Payload: payload})
		}
		snapshot(42)
		send(sdk.Event{Event: "willAppear", Context: "cpu", Action: "system.cpu"})
		wait(3200 * time.Millisecond)
		snapshot(57)
		wait(300 * time.Millisecond)
		send(sdk.Event{Event: "willDisappear", Context: "cpu"})
		wait(300 * time.Millisecond)
	case "persistence":
		counter := func(kind string, step int) {
			payload, _ := json.Marshal(map[string]any{"settings": map[string]any{"step": step}})
			send(sdk.Event{Event: kind, Context: "counter", Action: "counter.increment", Payload: payload})
		}
		counter("willAppear", 1)
		for range 80 {
			counter("keyDown", 1)
			time.Sleep(5 * time.Millisecond)
		}
		wait(350 * time.Millisecond)
		send(sdk.Event{Event: "acceptanceCheckpoint"})
		counter("didReceiveSettings", 0)
		for range 80 {
			counter("keyDown", 0)
			time.Sleep(5 * time.Millisecond)
		}
		wait(350 * time.Millisecond)
		send(sdk.Event{Event: "acceptanceCheckpoint"})
		counter("didReceiveSettings", 1)
		counter("keyDown", 1) // Normal shutdown must flush this final pending change
	default:
		count := 32
		if name == "idle" {
			count = 0
		}
		if name == "failing_unique" {
			count = 48
		}
		for i := 0; i < count; i++ {
			input("willAppear", fmt.Sprint(i), 0, name == "failing_unique")
			time.Sleep(2 * time.Millisecond)
		}
		if name == "rapid_reconfiguration" {
			end := time.Now().Add(6 * time.Second)
			for generation := 1; time.Now().Before(end); generation++ {
				id := fmt.Sprint(generation % 32)
				input("didReceiveSettings", id, generation, false)
				if generation%3 == 0 {
					input("willDisappear", id, generation, false)
					input("willAppear", id, generation+1, false)
				}
				ping()
				time.Sleep(20 * time.Millisecond)
			}
			wait(500 * time.Millisecond)
		} else {
			wait(6500 * time.Millisecond)
		}
		for i := 0; i < count; i++ {
			input("willDisappear", fmt.Sprint(i), 0, false)
		}
		wait(500 * time.Millisecond)
	}
	send(sdk.Event{Event: "shutdown"})
	if err := command.Wait(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, childOutput.String())
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("host reader did not stop")
	}
	data, err := os.ReadFile(childReport)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	sort.Float64s(latencies)
	record["ack_count"], record["protocol_outputs"], record["stale_feedback_violations"] = acks, outputs, stale
	record["matching_feedback_count"], record["pending_feedback_excluded_count"], record["unavailable_feedback_count"] = matchingFeedback, pendingFeedback, unavailableFeedback
	record["dispatch_ack_ms"] = latencies
	if len(latencies) > 0 {
		record["dispatch_ack_p95_ms"] = latencies[(len(latencies)-1)*95/100]
		record["dispatch_ack_max_ms"] = latencies[len(latencies)-1]
	}
	record["telemetry_titles"] = titles
	if record["peak_running_probes"].(float64) > 4 || record["peak_live_jobs"].(float64) > 32 {
		t.Error("production probe bounds exceeded")
	}
	if stale != 0 {
		t.Errorf("%d stale generation feedback messages", stale)
	}
	if acks == 0 || len(latencies) == 0 {
		t.Error("dispatch acknowledgments were not exercised")
	}
	if name == "visible_shared" || name == "failing_unique" || name == "rapid_reconfiguration" {
		if record["probe_calls"].(float64) == 0 {
			t.Error("visible workload did not exercise a probe")
		}
	}
	if (name == "visible_shared" || name == "rapid_reconfiguration") && matchingFeedback == 0 {
		t.Error("no current-generation feedback reached a settled consumer")
	}
	if name == "failing_unique" && unavailableFeedback == 0 {
		t.Error("no unavailable feedback reached consumers")
	}
	if record["remaining_active_probes"].(float64) != 0 || record["remaining_jobs"].(float64) != 0 {
		t.Error("shutdown retained probes/jobs")
	}
	if name == "persistence" {
		checkpoints := record["checkpoint_write_counts"].([]any)
		if len(checkpoints) != 2 || checkpoints[0] != checkpoints[1] {
			t.Error("no-op counter inputs wrote state")
		}
		writes := record["state_write_seconds"].([]any)
		if len(writes) == 0 || writes[0].(float64) < .245 || len(writes) >= 80 {
			t.Error("routine writes were not batched in the fixed window")
		}
		mutations := record["mutation_inputs_at_write"].([]any)
		if len(mutations) == 0 || mutations[0].(float64) <= 0 || mutations[0].(float64) >= 80 {
			t.Error("no routine flush occurred before the sustained 80-input burst ended")
		}
		if record["final_state"].(map[string]any)["counter"].(map[string]any)["value"] != float64(81) {
			t.Errorf("shutdown counter roundtrip failed: %v", record["final_state"])
		}
	}
	if name == "telemetry_expiry" {
		hasFresh, hasStale, hasRecovery := false, false, false
		for _, event := range titles {
			title := event["title"].(string)
			hasFresh = hasFresh || strings.Contains(title, "42.0%")
			hasStale = hasStale || strings.Contains(title, "Stale")
			hasRecovery = hasRecovery || strings.Contains(title, "57.0%")
		}
		if !hasFresh || !hasStale || !hasRecovery {
			t.Error("telemetry did not render fresh, expired, and recovered values")
		}
	}
	record["passed"] = !t.Failed()
	data, err = json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(destination, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

type Engine = engine.Engine
