package catalog

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// SupportPolicy is frozen into build and release snapshots. Retirements are
// explicit so a partial build never removes unrelated catalog entries
type SupportPolicy struct {
	SchemaVersion int                 `json:"schema_version"`
	RetiredIDs    []string            `json:"retired_ids"`
	Platforms     map[string][]string `json:"platforms"`
}

//go:embed support-policy.json
var supportJSON []byte

// Policy returns the declared platform support policy
func Policy() SupportPolicy {
	var policy SupportPolicy
	if err := json.Unmarshal(supportJSON, &policy); err != nil {
		panic(err)
	}
	return policy
}

// SupportedPlatforms lists the platforms enabled by that policy
func SupportedPlatforms(slug string) []string {
	policy := Policy()
	if slices.Contains(policy.RetiredIDs, ID(slug)) {
		return []string{}
	}
	if platforms, ok := policy.Platforms[ID(slug)]; ok {
		return platforms
	}
	return []string{"macos", "windows", "linux"}
}

// Supports reports whether the requested platform is supported
func Supports(slug, platform string) bool {
	if platform == "darwin" {
		platform = "macos"
	}
	return slices.Contains(SupportedPlatforms(slug), platform)
}
