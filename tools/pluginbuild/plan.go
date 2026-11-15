package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	official "github.com/StreamDockManager/Plugins/internal/catalog"
)

type planDefinition struct {
	official.Definition
	ID                 string           `json:"id"`
	Capabilities       sdk.Capabilities `json:"capabilities"`
	SupportedPlatforms []string         `json:"supported_platforms"`
}

type releasePlan struct {
	SchemaVersion int                    `json:"schema_version"`
	SupportPolicy official.SupportPolicy `json:"support_policy"`
	Repository    string                 `json:"repository"`
	Channel       string                 `json:"channel"`
	Commit        string                 `json:"commit"`
	Tag           string                 `json:"tag"`
	Selected      []string               `json:"selected"`
	Source        []planDefinition       `json:"source"`
	Baseline      struct {
		SchemaVersion int    `json:"schema_version"`
		Product       string `json:"product"`
		Repository    string `json:"repository"`
		Channel       string `json:"channel"`
	} `json:"baseline"`
}

// validatePlan validates the build plan before compilation or packaging
func validatePlan(b []byte, definitions []official.Definition, environment []string) (*releasePlan, map[string]bool, []string, error) {
	var plan releasePlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return nil, nil, nil, err
	}
	if plan.SchemaVersion != 1 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(plan.Repository) || (plan.Channel != "stable" && plan.Channel != "development") || !regexp.MustCompile(`^[a-f0-9]{40,64}$`).MatchString(plan.Commit) {
		return nil, nil, nil, fmt.Errorf("invalid release plan schema, repository, channel, or commit")
	}
	prefix, suffix := "plugins-"+plan.Channel+"-", "-"+plan.Commit[:12]
	if !strings.HasPrefix(plan.Tag, prefix) || !strings.HasSuffix(plan.Tag, suffix) || len(plan.Tag) <= len(prefix)+len(suffix) || !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(plan.Tag[len(prefix):len(plan.Tag)-len(suffix)]) {
		return nil, nil, nil, fmt.Errorf("release plan tag differs from channel/commit")
	}
	if plan.Baseline.SchemaVersion != 1 || plan.Baseline.Product != "plugins" || plan.Baseline.Repository != plan.Repository || plan.Baseline.Channel != plan.Channel {
		return nil, nil, nil, fmt.Errorf("release plan baseline identity differs")
	}
	if !reflect.DeepEqual(plan.SupportPolicy, official.Policy()) {
		return nil, nil, nil, fmt.Errorf("release plan support policy differs from current source")
	}
	source := map[string]planDefinition{}
	for _, p := range plan.Source {
		if p.ID != official.ID(p.Slug) || source[p.ID].ID != "" {
			return nil, nil, nil, fmt.Errorf("invalid or duplicate release plan source: %s", p.ID)
		}
		source[p.ID] = p
	}
	if len(source) != len(definitions) {
		return nil, nil, nil, fmt.Errorf("release plan source does not cover current plugins")
	}
	for _, d := range definitions {
		p, found := source[official.ID(d.Slug)]
		publicDefinition := d
		publicDefinition.ViewTargets = nil
		publicDefinition.RuntimeAssets = nil // Private packaging metadata is not part of a saved public plan
		if !found || !reflect.DeepEqual(p.Definition, publicDefinition) || !reflect.DeepEqual(p.Capabilities, official.Descriptor(d)) || !reflect.DeepEqual(p.SupportedPlatforms, official.SupportedPlatforms(d.Slug)) {
			return nil, nil, nil, fmt.Errorf("source differs from plan: %s", d.Slug)
		}
	}
	selection := map[string]bool{}
	for _, id := range plan.Selected {
		if source[id].ID == "" || selection[id] {
			return nil, nil, nil, fmt.Errorf("unknown or duplicate release plan selection: %s", id)
		}
		selection[id] = true
	}
	if len(selection) == 0 {
		return nil, nil, nil, fmt.Errorf("release plan has no version increases")
	}
	values := map[string]string{"RELEASE_REPOSITORY": plan.Repository, "RELEASE_CHANNEL": plan.Channel, "RELEASE_COMMIT": plan.Commit, "RELEASE_TAG": plan.Tag, "RELEASE_BUILD": plan.Tag}
	for key, value := range values {
		if configured := environmentValue(environment, key); configured != "" && configured != value {
			return nil, nil, nil, fmt.Errorf("%s differs from release plan", key)
		}
	}
	return &plan, selection, withEnvironment(environment, values), nil
}
