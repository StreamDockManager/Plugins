//go:build windows

package spotify

import (
	"runtime"
	"strings"
	"testing"
)

// TestHStringValueCopiesNativeUTF16BeforeRelease verifies h string value copies native utf16 before release
func TestHStringValueCopiesNativeUTF16BeforeRelease(t *testing.T) {
	for _, want := range []string{"", "Spotify", "Spötify 🎵", strings.Repeat("Ω音", 1024)} {
		h, err := hstring(want)
		if err != nil {
			t.Fatal(err)
		}
		got := hstringValue(h)
		hdelete.Call(h)
		runtime.GC()
		if got != want {
			t.Fatalf("HSTRING copy changed UTF-16 content: got %q, want %q", got, want)
		}
	}
	if got := hstringValue(0); got != "" {
		t.Fatalf("null HSTRING returned %q", got)
	}
}
