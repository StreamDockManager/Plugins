package main

import (
	"errors"
	"strings"
	"testing"
)

// TestUniversalToolSelection verifies universal tool selection
func TestUniversalToolSelection(t *testing.T) {
	for _, test := range []struct{ host, tool string }{{"linux", "llvm-lipo"}, {"darwin", "lipo"}} {
		t.Run(test.host, func(t *testing.T) {
			path, err := universalTool(test.host, func(name string) (string, error) {
				if name != test.tool {
					t.Fatalf("selected %s instead of %s", name, test.tool)
				}
				return "/tools/" + name, nil
			})
			if err != nil || path != "/tools/"+test.tool {
				t.Fatalf("tool selection: %s, %v", path, err)
			}
			_, err = universalTool(test.host, func(string) (string, error) { return "", errors.New("missing") })
			if err == nil || !strings.Contains(err.Error(), test.tool+" on PATH") {
				t.Fatalf("missing tool must fail clearly: %v", err)
			}
		})
	}
	if _, err := universalTool("windows", nil); err == nil {
		t.Fatal("unsupported universal build host accepted")
	}
}
