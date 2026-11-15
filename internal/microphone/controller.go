// Package microphone contains device-scoped, verified microphone controls
package microphone

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type Device struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Muted bool    `json:"muted"`
	Mode  string  `json:"mode"`
	Gain  float32 `json:"gain"`
}
type Adapter interface {
	Devices(context.Context) ([]Device, error)
	Default(context.Context) (string, error)
	Read(context.Context, string) (Device, error)
	SetMute(context.Context, string, bool) error
}
type Controller struct {
	Adapter Adapter
	mu      sync.Mutex
}

// Resolve selects the requested microphone device
func (c *Controller) Resolve(ctx context.Context, selected string) (Device, error) {
	if selected == "" || selected == "default" {
		id, err := c.Adapter.Default(ctx)
		if err != nil {
			return Device{}, err
		}
		selected = id
	}
	return c.Adapter.Read(ctx, selected)
}

// Apply serializes read-modify-verify, including default resolution. It never touches outputs
func (c *Controller) Apply(ctx context.Context, selected, action string) (Device, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.Resolve(ctx, selected)
	if err != nil {
		return d, err
	}
	target := false
	switch action {
	case "mute":
		target = true
	case "unmute":
		target = false
	case "toggle":
		target = !d.Muted
	default:
		return d, errors.New("unknown microphone action")
	}
	if target != d.Muted {
		if err = c.Adapter.SetMute(ctx, d.ID, target); err != nil {
			return d, err
		}
	}
	actual, err := c.Adapter.Read(ctx, d.ID)
	if err != nil {
		return actual, err
	}
	if actual.Muted != target {
		return actual, fmt.Errorf("microphone did not confirm requested mute state")
	}
	return actual, nil
}
