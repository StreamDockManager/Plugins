//go:build linux

package microphone

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type native struct{}

// Native constructs the platform-native microphone adapter
func Native(state string) Adapter { return native{} }

// pulse runs the bounded PulseAudio command
func pulse(ctx context.Context, args ...string) (string, error) {
	b, e := exec.CommandContext(ctx, "pactl", args...).CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("PulseAudio source service: %w (%s)", e, strings.TrimSpace(string(b)))
	}
	return strings.TrimSpace(string(b)), nil
}

// Default returns the default input device
func (n native) Default(ctx context.Context) (string, error) { return pulse(ctx, "get-default-source") }

// Devices lists the available microphone devices
func (n native) Devices(ctx context.Context) ([]Device, error) {
	s, e := pulse(ctx, "--format=json", "list", "sources")
	if e != nil {
		return nil, e
	}
	var rows []struct {
		Volume        map[string]struct{ Value uint32 } `json:"volume"`
		Name          string
		Description   string
		Mute          bool
		MonitorOfSink any `json:"monitor_of_sink"`
	}
	if e = json.Unmarshal([]byte(s), &rows); e != nil {
		return nil, e
	}
	out := []Device{}
	for _, r := range rows {
		if strings.HasSuffix(r.Name, ".monitor") {
			continue
		}
		var scalar float32
		for _, channel := range r.Volume {
			scalar += float32(channel.Value) / 65536
		}
		if len(r.Volume) > 0 {
			scalar /= float32(len(r.Volume))
		}
		out = append(out, Device{ID: r.Name, Name: r.Description, Muted: r.Mute, Mode: "hardware mute", Gain: scalar})
	}
	return out, nil
}

// Read reads the requested data through the owned adapter
func (n native) Read(ctx context.Context, id string) (Device, error) {
	ds, e := n.Devices(ctx)
	if e != nil {
		return Device{}, e
	}
	for _, d := range ds {
		if d.ID == id {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("selected microphone unavailable: %s", id)
}

// SetMute applies the requested microphone mute state
func (n native) SetMute(ctx context.Context, id string, m bool) error {
	d, err := n.Read(ctx, id)
	if err != nil {
		return err
	}
	return n.setResolvedMute(ctx, d, m)
}

func (n native) setResolvedMute(ctx context.Context, d Device, m bool) error {
	v := "0"
	if m {
		v = "1"
	}
	_, e := pulse(ctx, "set-source-mute", d.ID, v)
	return e
}
