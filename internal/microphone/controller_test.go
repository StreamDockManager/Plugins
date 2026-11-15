package microphone

import (
	"context"
	"errors"
	"testing"
)

type fake struct {
	devices map[string]Device
	def     string
	writes  int
	fail    bool
	ignore  bool
}

// Devices lists the available microphone devices
func (f *fake) Devices(context.Context) ([]Device, error) {
	var out []Device
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out, nil
}

// Default returns the default input device
func (f *fake) Default(context.Context) (string, error) { return f.def, nil }

// Read reads the requested data through the owned adapter
func (f *fake) Read(_ context.Context, id string) (Device, error) {
	d, ok := f.devices[id]
	if !ok {
		return d, errors.New("unavailable")
	}
	return d, nil
}

// SetMute applies the requested microphone mute state
func (f *fake) SetMute(_ context.Context, id string, m bool) error {
	if f.fail {
		return errors.New("permission denied")
	}
	f.writes++
	if !f.ignore {
		d := f.devices[id]
		d.Muted = m
		f.devices[id] = d
	}
	return nil
}

// TestMicrophoneActualStateAndDeviceIdentity verifies microphone actual state and device identity
func TestMicrophoneActualStateAndDeviceIdentity(t *testing.T) {
	ctx := context.Background()
	f := &fake{def: "a", devices: map[string]Device{"a": {ID: "a"}, "b": {ID: "b"}}}
	c := Controller{Adapter: f}
	for _, a := range []string{"mute", "mute", "unmute", "unmute", "toggle", "toggle"} {
		if _, e := c.Apply(ctx, "default", a); e != nil {
			t.Fatal(e)
		}
	}
	if f.writes != 4 {
		t.Fatal("idempotence", f.writes)
	}
	d := f.devices["a"]
	d.Muted = true
	f.devices["a"] = d
	if d, e := c.Apply(ctx, "a", "toggle"); e != nil || d.Muted {
		t.Fatal("external state", d, e)
	}
	f.def = "b"
	if d, e := c.Apply(ctx, "default", "mute"); e != nil || d.ID != "b" {
		t.Fatal(d, e)
	}
	delete(f.devices, "b")
	if _, e := c.Apply(ctx, "b", "toggle"); e == nil {
		t.Fatal("missing selection must not fall back")
	}
	f.fail = true
	if _, e := c.Apply(ctx, "a", "mute"); e == nil {
		t.Fatal("permission failure hidden")
	}
	f.fail = false
	f.ignore = true
	if _, e := c.Apply(ctx, "a", "mute"); e == nil {
		t.Fatal("unverified success")
	}
}
