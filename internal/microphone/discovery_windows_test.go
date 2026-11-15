//go:build windows

package microphone

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNativeMicrophoneDiscoveryReadOnly verifies native microphone discovery read only
func TestNativeMicrophoneDiscoveryReadOnly(t *testing.T) {
	if os.Getenv("SDM_NATIVE_MICROPHONE_DISCOVERY") != "1" {
		t.Skip("opt-in enumeration of this Windows host's microphone inputs; no recording or mute changes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := Native(filepath.Join(t.TempDir(), "gains.json"))
	devices, err := adapter.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, device := range devices {
		if device.ID == "" || device.Name == "" || seen[device.ID] {
			t.Fatal("native input enumeration returned empty/duplicate identity or name")
		}
		seen[device.ID] = true
		actual, err := adapter.Read(ctx, device.ID)
		if err != nil {
			t.Errorf("Read discovered input %q: %v", device.Name, err)
		} else if actual.ID != device.ID || actual.Muted != device.Muted {
			t.Errorf("Read disagrees with discovered input %q", device.Name)
		}
	}
	if id, err := adapter.Default(ctx); err == nil && !seen[id] {
		t.Fatal("default microphone is missing from active inputs")
	}
	t.Logf("Detected %d named microphone inputs without changing mute state", len(devices))
}

// TestNativeMicrophoneMuteRoundTrip verifies native microphone mute round trip
func TestNativeMicrophoneMuteRoundTrip(t *testing.T) {
	if os.Getenv("SDM_NATIVE_MICROPHONE_MUTE") != "1" {
		t.Skip("opt-in mute/readback/restore of the Windows default microphone")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := Native(filepath.Join(t.TempDir(), "gains.json"))
	id, err := adapter.Default(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := adapter.Read(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := adapter.SetMute(context.Background(), id, before.Muted); err != nil {
			t.Errorf("restore original microphone state: %v", err)
		}
	})
	controller := Controller{Adapter: adapter}
	for _, action := range []string{"mute", "unmute", "toggle", "toggle"} {
		actual, err := controller.Apply(ctx, id, action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		t.Logf("%s: verified muted=%v", action, actual.Muted)
	}
}
