//go:build !windows

package feishu

import "os/exec"

func applyCommandPlatformOptions(cmd *exec.Cmd) {}

func applyWindowsRawCommandLine(cmd *exec.Cmd, cmdLine string) {}
