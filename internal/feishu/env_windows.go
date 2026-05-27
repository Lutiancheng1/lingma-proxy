//go:build windows

package feishu

import (
	"os/exec"
	"syscall"
)

const windowsCreateNoWindow = 0x08000000

func applyCommandPlatformOptions(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windowsCreateNoWindow
}

func applyWindowsRawCommandLine(cmd *exec.Cmd, cmdLine string) {
	if cmdLine == "" {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = cmdLine
}
