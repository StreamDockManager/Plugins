package tailscale

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const fixture = `{"BackendState":"Running","Self":{"HostName":"desktop"},"Peer":{"node":{"ID":"n1","HostName":"exit","DNSName":"exit.example.","TailscaleIPs":["100.64.0.2"],"Online":true,"ExitNodeOption":true}}}`

// Verify discovery and preference preserving commands
func TestDiscoveryAndPreferencePreservingCommands(t *testing.T) {
	var calls [][]string
	d := &Driver{Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		if args[0] == "status" {
			return fixture, nil
		}
		return "", nil
	}}
	ctx := context.Background()
	s := map[string]any{"exit_node": "node", "peer": "node"}
	choices, err := d.Discover(ctx, s)
	if err != nil || len(choices) != 2 {
		t.Fatal(choices, err)
	}
	if _, err = d.Command(ctx, "exit-node", s, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[len(calls)-1], []string{"tailscale", "set", "--exit-node=100.64.0.2"}) {
		t.Fatal(calls)
	}
	if _, err = d.Command(ctx, "clear-exit-node", s, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[len(calls)-1], []string{"tailscale", "set", "--exit-node="}) {
		t.Fatal(calls)
	}
	v, _ := d.Read(ctx, s)
	if !strings.Contains(d.Display(v, "peer", s), "Online") {
		t.Fatal(v)
	}
}

// Verify login malformed and permission failures
func TestLoginMalformedAndPermissionFailures(t *testing.T) {
	for _, text := range []string{`{}`, `oops`, `{"BackendState":"NeedsLogin"}`} {
		calls := 0
		d := &Driver{Run: func(context.Context, string, ...string) (string, error) { calls++; return text, nil }}
		if _, err := d.Command(context.Background(), "connect", nil, 0); err == nil || calls != 1 {
			t.Fatal(text, calls, err)
		}
	}
	denied := errors.New("permission denied")
	d := &Driver{Run: func(context.Context, string, ...string) (string, error) { return "", denied }}
	if _, err := d.Read(context.Background(), nil); !errors.Is(err, denied) {
		t.Fatal(err)
	}
}
