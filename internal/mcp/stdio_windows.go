package mcp

import (
	"os/exec"
	"syscall"
)

// hide keeps a connector's command from opening a console window of its
// own when Keystone runs with none, as it does when started at sign-in.
func hide(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
