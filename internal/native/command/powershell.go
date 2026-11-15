package command

// PowerShellArgs keeps the script as one argument; callers own script escaping
func PowerShellArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", script}
}
