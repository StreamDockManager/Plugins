package systemmonitor

import "testing"

// Verify disk path platform defaults and mount boundaries
func TestDiskPathPlatformDefaultsAndMountBoundaries(t *testing.T) {
	for _, value := range []string{"", "/"} {
		if got := resolveDiskPath(value, "windows", "D:", ""); got != `D:\` {
			t.Fatal(got)
		}
	}
	if got := resolveDiskPath("/", "linux", "D:", ""); got != "/" {
		t.Fatal(got)
	}
	if got := resolveDiskPath("", "windows", "", `E:\Windows`); got != `E:\` {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		mount, path, platform string
		want                  bool
	}{
		{`C:\`, `c:\Users\File`, "windows", true},
		{`C:\`, `D:\Data`, "windows", false},
		{`C:\Data`, `C:\Database`, "windows", false},
		{`\\Host\Share`, `\\host\share\folder`, "windows", true},
		{`\\Host\Share`, `\\Host\Share2\folder`, "windows", false},
		{"/", "/mnt/drive/file", "linux", true},
		{"/mnt/drive", "/mnt/drive2/file", "linux", false},
		{"/mnt/Drive", "/mnt/drive/file", "linux", false},
	} {
		if got := diskPathContainsOn(tc.mount, tc.path, tc.platform); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, value := range []string{`F:\Media`, `\\Server\Media\Songs`} {
		if got := resolveDiskPath(value, "windows", "D:", ""); got != value {
			t.Fatal(got)
		}
	}
}
