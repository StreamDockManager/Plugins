package catalog

import (
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	randomizer "github.com/StreamDockManager/Plugins/internal/plugins/randomizer-dice"
	"github.com/StreamDockManager/Plugins/internal/plugins/retroachievements"
	example "github.com/StreamDockManager/Plugins/internal/plugins/sdm-vault-example"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Return optional package setup declarations without changing portable capabilities
func PackageSetupSlots(slug string) any {
	if slug == randomizer.Definition.Slug {
		return randomizer.SetupSlots()
	}
	if slug == example.Slug {
		return example.SetupSlots()
	}
	if slug == retroachievements.Definition.Slug {
		return retroachievements.SetupSlots()
	}
	for _, d := range Definitions {
		if d.Slug == slug {
			slots, _ := metadata.VaultDeclarations(slug, ID(slug), d.Actions)
			if slots != nil {
				return slots
			}
			break
		}
	}
	return nil
}

// Base permissions retain feature-owned Vault declarations
func basePackagePermissions(slug string) *sdk.PermissionDeclaration {
	if slug == example.Slug {
		return example.PackagePermissions()
	}
	if slug == retroachievements.Definition.Slug {
		return retroachievements.PackagePermissions()
	}
	for _, d := range Definitions {
		if d.Slug == slug {
			_, permissions := metadata.VaultDeclarations(slug, ID(slug), d.Actions)
			return permissions
		}
	}
	return nil
}

// PackagePermissions requires approval for Manager action failure notifications
func PackagePermissions(slug string) *sdk.PermissionDeclaration {
	for _, d := range Definitions {
		if d.Slug != slug {
			continue
		}
		result := &sdk.PermissionDeclaration{Version: 1, Actions: map[string]sdk.ActionPermissions{}}
		if base := basePackagePermissions(slug); base != nil {
			result.Version = base.Version
			for action, scope := range base.Actions {
				result.Actions[action] = sdk.ActionPermissions{Purpose: scope.Purpose, Required: clonePermissionScopes(scope.Required), Optional: clonePermissionScopes(scope.Optional)}
			}
		}
		for _, action := range d.Actions {
			id := ID(slug) + "." + action
			scope := result.Actions[id]
			if scope.Purpose == "" {
				scope.Purpose = "Show action failures in Manager without including private settings or service responses"
			}
			scope.Required = append(scope.Required, map[string]string{"id": "notifications.create", "mode": "plugin-message"})
			result.Actions[id] = scope
		}
		return result
	}
	return nil
}

// Copy mutable permission maps so descriptor consumers cannot modify future declarations
func clonePermissionScopes(scopes []map[string]string) []map[string]string {
	if scopes == nil {
		return nil
	}
	result := make([]map[string]string, len(scopes))
	for i, scope := range scopes {
		result[i] = map[string]string{}
		for key, value := range scope {
			result[i][key] = value
		}
	}
	return result
}

func setVaultDescriptor(descriptor *sdk.Capabilities, slug string) {
	descriptor.Permissions = PackagePermissions(slug)
}
