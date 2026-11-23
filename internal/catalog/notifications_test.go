package catalog

import (
	"reflect"
	"testing"
)

// Every official action requires notification permission while preserving feature permissions
func TestNotificationPermissionsPreserveExistingScopes(t *testing.T) {
	for _, d := range Definitions {
		p := PackagePermissions(d.Slug)
		if p == nil || p.Version != 1 || d.MinimumManager != "0.1.1" {
			t.Fatal(d.Slug, p, d.MinimumManager)
		}
		base := basePackagePermissions(d.Slug)
		for _, action := range d.Actions {
			id := ID(d.Slug) + "." + action
			scope := p.Actions[id]
			n := 0
			for _, permission := range scope.Required {
				if permission["id"] == "notifications.create" && permission["mode"] == "plugin-message" {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%s notification declarations = %d", id, n)
			}
			for _, permission := range scope.Optional {
				if permission["id"] == "notifications.create" {
					t.Fatal("optional notification", id)
				}
			}
			if base != nil {
				before := base.Actions[id]
				if !reflect.DeepEqual(before.Optional, scope.Optional) || !reflect.DeepEqual(before.Required, scope.Required[:len(scope.Required)-1]) && len(before.Required) != 0 {
					t.Fatal("changed feature permissions", id)
				}
			}
		}
		first := ID(d.Slug) + "." + d.Actions[0]
		p.Actions[first].Required[0]["id"] = "mutated"
		for _, permission := range PackagePermissions(d.Slug).Actions[first].Required {
			if permission["id"] == "mutated" {
				t.Fatal("shared permission storage", d.Slug)
			}
		}
	}
	if PackagePermissions("missing") != nil {
		t.Fatal("unknown plugin permissions")
	}
}
