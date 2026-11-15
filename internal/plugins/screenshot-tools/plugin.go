package screenshottools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/native/capture"
)

type Plugin struct {
	*engine.Engine
	captureScreenshot func(context.Context, string, string) error
}

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

// Attach screenshot tools lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Initialize screenshot tools state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base}
	return e
}

// Apply completed screenshot tools work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {
	return e.FinishJob(ctx, c, result, job)
}

// Build the current screenshot tools status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	text, err := e.text(ctx, id, b)
	return engine.TextStatus("screenshot-tools", action(b.Action), text, err)
}

// Route screenshot tools inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	return e.InspectBasic(ctx, c, ev)
}

// Dispatch an activating screenshot tools input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	s := b.Settings
	if !active(ev) && ev.Event != "dialRotate" {
		return nil
	}
	a := action(ev.Action)

	if ev.Event == "dialRotate" {
		delete(e.Effective, ev.Context)

		return nil

	}
	if !active(ev) {
		return nil
	}
	{

		if a == "clipboard" {
			e.startCapture(ctx, ev.Context, a, "")
			return e.Render(ctx, c, ev.Context)
		}
		format := configvalue.String(s, "format", "png")
		if format != "png" && format != "jpg" {
			return fmt.Errorf("choose PNG or JPG")
		}
		path := configvalue.String(s, "path", "")
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, "Pictures", "Stream Dock Manager", time.Now().Format("20060102-150405.000")+"."+format)
		}
		path = strings.TrimSuffix(path, filepath.Ext(path)) + "." + format
		e.startCapture(ctx, ev.Context, a, path)
	}

	return e.Render(ctx, c, ev.Context)
}

// Expose the screenshot tools feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Apply: e.applyResult, Visuals: visuals}
}

// Choose the next explicit screenshot tools deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	return e.PendingWrite

}

// Compute the screenshot tools text shown for the selected action
func (e *Plugin) text(_ context.Context, id string, _ binding) (string, error) {

	if text := e.ResultsText[id]; text != "" {
		return text, nil
	}
	return "screenshot-tools", nil
}

// Capture owns only its temporary output until the action succeeds. Cancellation
// cannot remove or replace an existing destination file
func (e *Plugin) startCapture(ctx context.Context, id, mode, path string) bool {
	captureWork := e.captureScreenshot
	if captureWork == nil {
		captureWork = capture.Screenshot
	}
	return e.StartJob(ctx, id, func(job context.Context) (string, any, error) {
		if mode == "clipboard" {
			directory, err := os.MkdirTemp("", "stream-dock-manager-clipboard-*")
			if err != nil {
				return "", nil, err
			}
			defer os.RemoveAll(directory)
			if err = captureWork(job, mode, filepath.Join(directory, "clipboard.png")); err != nil {
				return "", nil, err
			}
			return "Copied to clipboard", nil, job.Err()
		}
		if !filepath.IsAbs(path) {
			return "", nil, fmt.Errorf("capture path must be absolute")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", nil, err
		}
		temp, err := os.CreateTemp(filepath.Dir(path), ".capture-*"+filepath.Ext(path))
		if err != nil {
			return "", nil, err
		}
		name := temp.Name()
		temp.Close()
		defer os.Remove(name)
		if err = captureWork(job, mode, name); err != nil {
			return "", nil, err
		}
		if err = job.Err(); err != nil {
			return "", nil, err
		}
		// Interactive helpers can exit successfully without producing an image
		// The precreated temporary file must never replace a user's destination
		// when a selection was cancelled or no capture was written
		info, err := os.Stat(name)
		if err != nil {
			return "", nil, err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return "", nil, fmt.Errorf("capture cancelled or no image produced")
		}
		if err = os.Rename(name, path); err != nil {
			return "", nil, err
		}
		return "Capture completed", nil, nil
	}, e.applyResult)
}
