package engine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
)

type observationMarker struct{}

// observationRecorder records observation behavior for lifecycle assertions
func observationRecorder(t *testing.T) *testkit.Recorder {
	t.Helper()
	host, err := sdk.ParseHostInfo([]byte(`{"streamDockManager":{"contract_version":1,"capabilities":["plugin-protocol-v1","plugin-logs-v1","action-results-v1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	return testkit.New(host)
}

// requireObservationCommandSuccess requires the observed command to have completed successfully
func requireObservationCommandSuccess(t *testing.T, events []sdk.Event) {
	t.Helper()
	receipts := 0
	for _, event := range events {
		if event.Event == "actionResult" {
			var payload struct{ Success bool }
			if err := json.Unmarshal(event.Payload, &payload); err != nil || !payload.Success {
				t.Fatal("background observation changed command outcome", event, err)
			}
			receipts++
		}
		if event.Event == "logMessage" && strings.Contains(string(event.Payload), "failed or cancelled") {
			t.Fatal("background observation failed the command", event)
		}
	}
	if receipts != 1 {
		t.Fatal("command must finish before its background observation", receipts)
	}
}

// TestObservationOutcomesKeepCommandReceiptAndOriginalViewScope verifies observation outcomes keep command receipt and original view scope
func TestObservationOutcomesKeepCommandReceiptAndOriginalViewScope(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			e := New(Definition{ViewTargets: []ViewTarget{{Action: "command"}, {Action: "status", UpdatedBy: []string{"command"}, Live: true}, {Action: "other"}}}, filepath.Join(t.TempDir(), "state.json"), func(*Engine) Feature { return Feature{} })
			defer e.Close()
			for _, id := range []string{"command", "status", "other"} {
				e.Bindings[id] = Binding{Action: id}
			}
			recorder := observationRecorder(t)
			release := make(chan struct{})
			var workErr error
			if failure {
				workErr = errors.New("status unavailable")
			}
			applied := false
			e.Feature.Action = func(ctx context.Context, _ *sdk.Client, _ sdk.Event, _ Binding) error {
				work := func(ctx context.Context) (string, any, error) {
					if ctx.Value(observationMarker{}) != nil {
						return "", nil, errors.New("worker retained command-specific context")
					}
					select {
					case <-release:
						return "", nil, workErr
					case <-ctx.Done():
						return "", nil, ctx.Err()
					}
				}
				if !StartObservationJob(ctx, e, "status-read", work, func(ctx context.Context, c *sdk.Client, result JobResult, _ BackgroundJob) error {
					applied = true
					if !errors.Is(result.Err, workErr) || ctx.Value(observationMarker{}) != "original" {
						t.Fatal("observation lost its result or originating context", result.Err)
					}
					for id := range e.Bindings {
						if err := e.SendDisplayView(ctx, c, id, Display{Title: "Observed"}, false); err != nil {
							return err
						}
					}
					return nil
				}) {
					t.Fatal("observation did not start")
				}
				if StartObservationJob(ctx, e, "status-read", work, nil) {
					t.Fatal("duplicate observation was accepted")
				}
				return nil
			}
			ctx := context.WithValue(context.Background(), observationMarker{}, "original")
			event := sdk.Event{Event: "keyDown", Context: "command", Action: "command", Surface: &sdk.Surface{RequestID: "command"}}
			recorder.Client.Dispatch(ctx, e, event)
			requireObservationCommandSuccess(t, recorder.Drain())
			close(release)
			select {
			case <-e.Wake():
			case <-time.After(time.Second):
				t.Fatal("observation did not finish")
			}
			if err := e.DrainJobs(context.Background(), recorder.Client); err != nil {
				t.Fatal(err)
			}
			if !applied || len(e.Jobs) != 0 || e.Last["command"].Title != "Observed" || e.Last["status"].Title != "Observed" || e.Last["other"].Title != "" {
				t.Fatal("observation escaped the original view scope", e.Last)
			}
			for _, event := range recorder.Drain() {
				if event.Event == "actionResult" || event.Event == "logMessage" {
					t.Fatal("observation completed the command again", event)
				}
			}
		})
	}
}

// TestObservationRetainsCancellationAndDeadline verifies observation retains cancellation and deadline
func TestObservationRetainsCancellationAndDeadline(t *testing.T) {
	e := New(Definition{}, "", func(*Engine) Feature { return Feature{} })
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	if !StartObservationJob(ctx, e, "read", func(job context.Context) (string, any, error) {
		if got, ok := job.Deadline(); !ok {
			t.Error("observation lost its deadline")
		} else if want, _ := ctx.Deadline(); !got.Equal(want) {
			t.Error("observation changed its deadline")
		}
		close(started)
		<-job.Done()
		close(stopped)
		return "", nil, job.Err()
	}, nil) {
		t.Fatal("observation did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("observation worker did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("observation ignored cancellation")
	}
}

// TestRejectedObservationDoesNotFailSuccessfulCommand verifies rejected observation does not fail successful command
func TestRejectedObservationDoesNotFailSuccessfulCommand(t *testing.T) {
	e := New(Definition{}, "", func(*Engine) Feature { return Feature{} })
	defer e.Close()
	for i := range 32 {
		e.Jobs[string(rune('a'+i))] = BackgroundJob{Cancel: func() {}}
	}
	e.Feature.Action = func(ctx context.Context, _ *sdk.Client, _ sdk.Event, _ Binding) error {
		if StartObservationJob(ctx, e, "read", func(context.Context) (string, any, error) {
			t.Error("rejected observation ran")
			return "", nil, nil
		}, nil) {
			t.Fatal("full queue accepted an observation")
		}
		return nil
	}
	recorder := observationRecorder(t)
	recorder.Client.Dispatch(context.Background(), e, sdk.Event{Event: "keyDown", Context: "command", Action: "command", Surface: &sdk.Surface{RequestID: "command"}})
	requireObservationCommandSuccess(t, recorder.Drain())
}
