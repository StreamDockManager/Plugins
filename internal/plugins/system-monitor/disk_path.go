package systemmonitor

import (
	"os"
	"path"
	"runtime"
	"strings"
)

// Resolve the selected disk path for the current platform
func resolvedDiskPath(value string) string {
	return resolveDiskPath(value, runtime.GOOS, os.Getenv("SystemDrive"), os.Getenv("SystemRoot"))
}

// Choose an explicit or platform-default disk monitoring path
func resolveDiskPath(value, platform, systemDrive, systemRoot string) string {
	if platform != "windows" {
		if strings.TrimSpace(value) == "" {
			return "/"
		}
		return path.Clean(value)
	}
	if strings.TrimSpace(value) == "" || value == "/" || value == `\` {
		value = systemDrive
		if value == "" && len(systemRoot) >= 2 && systemRoot[1] == ':' {
			value = systemRoot[:2]
		}
		if value == "" {
			value = "C:"
		}
		value = strings.TrimRight(value, `/\`) + `\`
	}
	return strings.ReplaceAll(value, "/", `\`)
}

// Check mount containment using current-platform path rules
func diskPathContains(mount, selected string) bool {
	return diskPathContainsOn(mount, selected, runtime.GOOS)
}

// Check mount containment without confusing sibling path prefixes
func diskPathContainsOn(mount, selected, platform string) bool {
	if platform == "windows" {
		// Preserve UNC roots while using slash-based cleaning independently of the
		// host running fixture tests. Windows paths compare case-insensitively
		clean := func(value string) string {
			return strings.ToLower(path.Clean(strings.ReplaceAll(value, `\`, "/")))
		}
		root, value := clean(mount), clean(selected)
		root = strings.TrimRight(root, "/")
		return value == root || strings.HasPrefix(value, root+"/")
	}
	root, value := path.Clean(mount), path.Clean(selected)
	return value == root || strings.HasPrefix(value, strings.TrimRight(root, "/")+"/")
}
