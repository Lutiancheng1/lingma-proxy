//go:build !windows

package feishu

import "os/exec"

func applyCommandPlatformOptions(cmd *exec.Cmd) {}
