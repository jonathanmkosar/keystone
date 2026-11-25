//go:build !windows

package mcp

import "os/exec"

// hide does nothing outside Windows, where a command opens no window.
func hide(*exec.Cmd) {}
