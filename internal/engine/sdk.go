// Package engine supplies official product identity to the SDK authoring runtime
package engine

import (
	"context"
	"fmt"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/authoring"
	"os"
	"os/signal"
	"syscall"
)

// VaultMinimumManager is the first Manager version with plugin Vault support
const VaultMinimumManager = "0.1.1"

// NotificationsMinimumManager is the first Manager version accepting permission declarations
const NotificationsMinimumManager = "0.1.1"

type Definition = authoring.Definition
type ViewTarget = authoring.ViewTarget
type Binding = authoring.Binding
type DisplayStatus = authoring.DisplayStatus
type Presentation = authoring.Presentation
type Display = authoring.Display
type Visual = authoring.Visual
type SampledStatus = authoring.SampledStatus
type Feature = authoring.Feature
type Factory = authoring.Factory
type Engine = authoring.Engine
type BackgroundJob = authoring.BackgroundJob
type JobResult = authoring.JobResult

var ValidateViewTargets = authoring.ValidateViewTargets
var CloneSettings = authoring.CloneSettings
var CloneFeedback = authoring.CloneFeedback
var DisplayLines = authoring.DisplayLines
var ResultText = authoring.ResultText
var Action = authoring.Action
var Active = authoring.Active

// ID constructs the fully qualified plugin identity
func ID(slug string) string { return "app.streamdockmanager.plugins." + slug }

// Main starts the registered plugin with the shared runtime
func Main(def Definition, factory Factory) {
	def.ID = ID(def.Slug)
	// Official runtime state is always owned by the connected host
	// Disposable file-backed constructors live in internal/fixtureengine
	e := newManaged(def, factory)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := sdk.Run(ctx, os.Args[1:], &managedHandler{Engine: e}); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
