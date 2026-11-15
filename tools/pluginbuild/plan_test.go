package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

// releasePlanFixture constructs a validated release-plan fixture
func releasePlanFixture(t *testing.T) (releasePlan, *buildRunner) {
	t.Helper()
	builder := packageFixture(t)
	builder.planFile = filepath.Join(builder.root, "release-plan.json")
	builder.planSelected, builder.activePlan = nil, nil
	builder.setenv("RELEASE_REPOSITORY", "example/plugins")
	commit := strings.Repeat("a", 40)
	plan := releasePlan{SupportPolicy: official.Policy(), SchemaVersion: 1, Repository: "example/plugins", Channel: "development", Commit: commit, Tag: "plugins-development-fixture-" + commit[:12], Selected: []string{official.ID(builder.sourceDefinitions[0].Slug)}}
	plan.Baseline.SchemaVersion, plan.Baseline.Product = 1, "plugins"
	plan.Baseline.Repository, plan.Baseline.Channel = plan.Repository, plan.Channel
	for _, d := range builder.sourceDefinitions {
		plan.Source = append(plan.Source, planDefinition{Definition: d, ID: official.ID(d.Slug), Capabilities: official.Descriptor(d), SupportedPlatforms: official.SupportedPlatforms(d.Slug)})
	}
	return plan, builder
}

// TestReleasePlanPreservesNativeCatalogProvenanceAndSelection verifies release plan preserves native catalog provenance and selection
func TestReleasePlanPreservesNativeCatalogProvenanceAndSelection(t *testing.T) {
	plan, builder := releasePlanFixture(t)
	if err := writeJSON(builder.planFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := builder.readPlan(); err != nil {
		t.Fatal(err)
	}
	if err := builder.packWithNotices("linux", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(builder.root, "dist", "catalog-linux.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Repository string           `json:"repository"`
		Channel    string           `json:"channel"`
		Commit     string           `json:"commit"`
		ReleaseTag string           `json:"release_tag"`
		Packages   []packagedPlugin `json:"packages"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Repository != plan.Repository || catalog.Channel != plan.Channel || catalog.Commit != plan.Commit || catalog.ReleaseTag != plan.Tag {
		t.Fatalf("native catalog lost release provenance: %+v", catalog)
	}
	if len(catalog.Packages) != 1 || catalog.Packages[0].ID != plan.Selected[0] {
		t.Fatalf("native catalog changed planned selection: %+v", catalog.Packages)
	}
}

// TestReleasePlanRejectsInconsistentInputsBeforePackaging verifies release plan rejects inconsistent inputs before packaging
func TestReleasePlanRejectsInconsistentInputsBeforePackaging(t *testing.T) {
	for name, change := range map[string]func(*releasePlan){
		"support policy":      func(p *releasePlan) { p.SupportPolicy.RetiredIDs = nil },
		"supported platforms": func(p *releasePlan) { p.Source[0].SupportedPlatforms = nil },
		"schema":              func(p *releasePlan) { p.SchemaVersion = 2 },
		"repository":          func(p *releasePlan) { p.Repository = "example/plugins/other" },
		"channel":             func(p *releasePlan) { p.Channel = "nightly" },
		"commit":              func(p *releasePlan) { p.Commit = "main" },
		"tag channel":         func(p *releasePlan) { p.Tag = strings.Replace(p.Tag, "development", "stable", 1) },
		"tag commit":          func(p *releasePlan) { p.Tag = strings.ReplaceAll(p.Tag, "aaaaaaaaaaaa", "bbbbbbbbbbbb") },
		"tag run":             func(p *releasePlan) { p.Tag = "plugins-development-" + p.Commit[:12] },
		"baseline":            func(p *releasePlan) { p.Baseline.Repository = "other/plugins" },
		"unknown plugin":      func(p *releasePlan) { p.Selected = []string{"app.streamdockmanager.plugins.unknown"} },
		"duplicate plugin":    func(p *releasePlan) { p.Selected = append(p.Selected, p.Selected[0]) },
		"empty selection":     func(p *releasePlan) { p.Selected = nil },
		"missing source":      func(p *releasePlan) { p.Source = p.Source[:1] },
		"duplicate source":    func(p *releasePlan) { p.Source[1] = p.Source[0] },
		"source identity":     func(p *releasePlan) { p.Source[0].ID = "app.streamdockmanager.plugins.other" },
		"source actions":      func(p *releasePlan) { p.Source[0].Actions = []string{"different"} },
		"source descriptor":   func(p *releasePlan) { p.Source[0].Capabilities.ThemeProtocol++ },
		"plugin override":     func(p *releasePlan) {},
		"environment":         func(p *releasePlan) {},
	} {
		t.Run(name, func(t *testing.T) {
			plan, builder := releasePlanFixture(t)
			if name == "environment" {
				builder.setenv("RELEASE_COMMIT", strings.Repeat("b", 40))
			}
			change(&plan)
			if name == "plugin override" {
				builder.selected = "counter-tally"
			}
			if err := writeJSON(builder.planFile, plan); err != nil {
				t.Fatal(err)
			}
			if err := builder.readPlan(); err == nil {
				t.Fatal("inconsistent release plan accepted")
			}
			if builder.activePlan != nil || builder.planSelected != nil {
				t.Fatal("invalid plan activated a release selection")
			}
		})
	}
}

// TestPrivateRuntimeAssetsAreNotRequiredInPublicReleasePlans verifies private runtime assets are not required in public release plans
func TestPrivateRuntimeAssetsAreNotRequiredInPublicReleasePlans(t *testing.T) {
	plan, builder := releasePlanFixture(t)
	builder.sourceDefinitions[0].RuntimeAssets = []string{"icons/unavailable"}
	if err := writeJSON(builder.planFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := builder.readPlan(); err != nil {
		t.Fatal(err)
	}
}
