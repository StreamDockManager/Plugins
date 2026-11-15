package microphonecontrols

import (
	"context"
	"encoding/json"
	"fmt"
	discovery "github.com/StreamDockManager/Plugin-SDK/Go/discovery"
	"sort"
	"strings"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

type microphoneChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Return bounded microphone choices through correlated inspector discovery
func (e *Plugin) discoverMicrophones(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
	var request struct {
		RequestID string `json:"request_id"`
		Scope     string `json:"scope"`
	}
	if json.Unmarshal(ev.Payload, &request) != nil || len(request.RequestID) > 80 {
		return fmt.Errorf("invalid microphone discovery request")
	}
	adapter := e.microphoneController().Adapter
	return e.discovery.Run(ctx, e.Engine, c, discovery.Request{Context: ev.Context, Action: ev.Action, Channel: "microphones", ID: request.RequestID, Operation: "microphones", Scope: request.Scope}, 5*time.Second, func(job context.Context) (map[string]any, error) {
		devices, err := adapter.Devices(job)
		choices := []microphoneChoice{}
		seen := map[string]bool{}
		for _, device := range devices {
			if device.ID == "" || device.ID == "default" || seen[device.ID] {
				continue
			}
			seen[device.ID] = true
			name := strings.TrimSpace(device.Name)
			if name == "" {
				name = "Unnamed microphone"
			}
			choices = append(choices, microphoneChoice{ID: device.ID, Name: name})
		}
		sort.Slice(choices, func(i, j int) bool {
			a, b := strings.ToLower(choices[i].Name), strings.ToLower(choices[j].Name)
			if a == b {
				return choices[i].ID < choices[j].ID
			}
			return a < b
		})
		defaultID := ""
		if err == nil {
			// A missing default does not prevent choosing another available input
			defaultID, _ = adapter.Default(job)
		}
		if job.Err() != nil {
			err = job.Err()
		}
		message := "Choose a microphone, or follow the system default."
		if err != nil {
			message = fmt.Sprintf("Could not detect microphones: %v", err)
		}
		return map[string]any{"devices": choices, "default_id": defaultID, "message": message}, err
	})
}
