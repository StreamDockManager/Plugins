package screenshottools

import (
	"os"
	"path/filepath"
	"testing"
)

// Verify session only feature has no persistence hooks
func TestSessionOnlyFeatureHasNoPersistenceHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte("existing state is not consumed or modified")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	e := New(path)
	if e.hooks().Snapshot != nil {
		t.Fatal("session-only feature exposes a persisted snapshot")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(original) {
		t.Fatalf("existing file changed: %q %v", actual, err)
	}
}
