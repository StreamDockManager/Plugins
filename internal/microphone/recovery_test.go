package microphone

import (
	"path/filepath"
	"testing"
)

// TestGainRecoveryPreservesDeviceAndChannelAcrossRestart verifies gain recovery preserves device and channel across restart
func TestGainRecoveryPreservesDeviceAndChannelAcrossRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "recovery.json")
	original := map[string]float32{"device-a/0": .73, "device-b/1": .2, "device-b/2": 0}
	if e := saveGains(p, original); e != nil {
		t.Fatal(e)
	}
	restored, e := loadGains(p)
	if e != nil {
		t.Fatal(e)
	}
	for key, want := range original {
		if got, ok := restored[key]; !ok || got != want {
			t.Fatal(key, got, want)
		}
	}
	if _, ok := restored["missing-device/0"]; ok {
		t.Fatal("invented restore gain")
	}
	missing, e := loadGains(filepath.Join(t.TempDir(), "missing"))
	if e != nil || len(missing) != 0 {
		t.Fatal(missing, e)
	}
}
