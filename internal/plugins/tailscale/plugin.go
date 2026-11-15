package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"github.com/StreamDockManager/Plugins/internal/connectors/command"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"sort"
	"strings"
	"time"
)

var Icons = map[string]string{"status": "shield", "peer": "network", "connect": "play", "disconnect": "stop", "toggle": "cycle", "exit-node": "network", "clear-exit-node": "cancel"}
var Definition, visuals = c.Define("tailscale", "Tailscale", Icons, "status", "peer", "connect", "disconnect", "toggle", "exit-node", "clear-exit-node")

// Declare the Tailscale global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Path("tailscale", "Tailscale executable", "tailscale", "file")}
}

// Declare settings supported by the selected Tailscale action
func ActionFields(a string) []settings.Field {
	if a == "peer" {
		return []settings.Field{metadata.Device("peer", "Peer ID or DNS name")}
	}
	if a == "exit-node" {
		return []settings.Field{metadata.Device("exit_node", "Advertised exit node")}
	}
	return nil
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 5 * time.Second, New: func(chan<- struct{}) c.Driver { return &Driver{Run: command.Run} }})

type Peer struct {
	ID             string
	HostName       string
	DNSName        string
	TailscaleIPs   []string
	Online         bool
	ExitNodeOption bool
	ExitNode       bool
}
type Status struct {
	BackendState string
	Self         *Peer
	Peer         map[string]Peer
}
type Driver struct{ Run c.Runner }

// Choose the shared Tailscale resource identity for these settings
func (*Driver) Key(map[string]any) string { return "tailnet" }

// Read the Tailscale resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	text, err := d.Run(ctx, c.String(s, "tailscale", "tailscale"), "status", "--json")
	if err != nil {
		return nil, err
	}
	var status Status
	if json.Unmarshal([]byte(text), &status) != nil || status.BackendState == "" {
		return nil, fmt.Errorf("malformed Tailscale status")
	}
	return status, nil
}

// Execute the selected Tailscale command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, _ int) (c.Outcome, error) {
	if a == "status" || a == "peer" {
		_, err := d.Read(ctx, s)
		return c.Outcome{Message: "Status refreshed"}, err
	}
	current, err := d.Read(ctx, s)
	if err != nil {
		return c.Outcome{}, err
	}
	status := current.(Status)
	if status.BackendState == "NeedsLogin" || status.BackendState == "NoState" {
		return c.Outcome{}, fmt.Errorf("sign in using Tailscale before controlling the connection")
	}
	var args []string
	switch a {
	case "connect":
		args = []string{"up"}
	case "disconnect":
		args = []string{"down"}
	case "toggle":
		if status.BackendState == "Running" {
			args = []string{"down"}
		} else {
			args = []string{"up"}
		}
	case "exit-node":
		selected := c.String(s, "exit_node", "")
		var node string
		for key, p := range status.Peer {
			if selected == key || selected == p.ID || selected == p.DNSName || selected == p.HostName {
				if p.ExitNodeOption && len(p.TailscaleIPs) > 0 {
					node = p.TailscaleIPs[0]
				}
			}
		}
		if node == "" {
			return c.Outcome{}, fmt.Errorf("select an advertised exit node using discovery")
		}
		args = []string{"set", "--exit-node=" + node}
	case "clear-exit-node":
		args = []string{"set", "--exit-node="}
	default:
		return c.Outcome{}, fmt.Errorf("unknown Tailscale action")
	}
	_, err = d.Run(ctx, c.String(s, "tailscale", "tailscale"), args...)
	return c.Outcome{Message: "Tailscale request delivered; refreshing status"}, err
}

// List Tailscale choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	value, err := d.Read(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []c.Choice
	for key, p := range value.(Status).Peer {
		out = append(out, c.Choice{ID: key, Name: p.HostName, Kind: "peer"})
		if p.ExitNodeOption {
			out = append(out, c.Choice{ID: key, Name: p.HostName, Kind: "exit_node"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID+out[i].Kind < out[j].ID+out[j].Kind })
	return out, nil
}

// Format a Tailscale resource sample for its selected action
func (*Driver) Display(value any, a string, s map[string]any) string {
	status := value.(Status)
	if a == "peer" {
		id := c.String(s, "peer", "")
		for key, p := range status.Peer {
			if id == key || id == p.ID || id == p.DNSName || id == p.HostName {
				if p.Online {
					return p.HostName + "\nOnline"
				}
				return p.HostName + "\nOffline"
			}
		}
		return "Selected peer unavailable"
	}
	var text strings.Builder
	text.WriteString("Tailscale\n")
	text.WriteString(status.BackendState)
	for _, p := range status.Peer {
		if p.ExitNode {
			text.WriteString("\nExit: ")
			text.WriteString(p.HostName)
		}
	}
	return text.String()
}

// Finish the stateless Tailscale driver without persistent resource cleanup
func (*Driver) Close() {}
