package engine

import configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"

// SavedKey preserves shared identities without creating or dirtying state
func SavedKey(id string, settings map[string]any) string {
	return configvalue.String(settings, "shared_id", id)
}

// LookupSaved looks up a retained named configuration entry
func LookupSaved[T any](states map[string]*T, id string, settings map[string]any, empty *T) *T {
	if value := states[SavedKey(id, settings)]; value != nil {
		return value
	}
	return empty
}

// EnsureSaved only creates a record. Callers own dirty marking and persistence
func EnsureSaved[T any](states map[string]*T, key string) *T {
	if value := states[key]; value != nil {
		return value
	}
	value := new(T)
	states[key] = value
	return value
}
