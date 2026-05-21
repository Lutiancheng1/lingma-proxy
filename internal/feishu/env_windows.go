//go:build windows

package feishu

import (
	"os/exec"
	"syscall"
)

const windowsCreateNoWindow = 0x08000000

func applyCommandPlatformOptions(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNoWindow,
	}
}
