package feishu

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

func detectBinary(name string, versionArgs ...string) BinaryStatus {
	path, err := exec.LookPath(name)
	if err != nil {
		return BinaryStatus{Found: false}
	}
	status := BinaryStatus{Found: true, Path: path}
	if len(versionArgs) > 0 {
		cmd := exec.Command(path, versionArgs...)
		if output, err := cmd.CombinedOutput(); err == nil {
			status.Version = strings.TrimSpace(string(output))
		}
	}
	return status
}

func detectNodeAndNPM() (BinaryStatus, BinaryStatus, BinaryStatus) {
	node := detectBinary("node", "--version")
	npm := detectBinary("npm", "--version")
	npx := detectBinary("npx", "--version")
	if !node.Found || !npm.Found || !npx.Found {
		hint := nodeInstallHint(runtime.GOOS)
		if !node.Found {
			node.Hint = hint
		}
		if !npm.Found {
			npm.Hint = hint
		}
		if !npx.Found {
			npx.Hint = hint
		}
	}
	return node, npm, npx
}

func nodeInstallHint(goos string) string {
	switch goos {
	case "darwin":
		return "请先安装 Node.js 16+（推荐 18+），例如通过 https://nodejs.org 或 Homebrew。"
	case "windows":
		return "请先安装 Node.js 16+（推荐 18+），例如通过 https://nodejs.org 或 winget。"
	default:
		return "请先安装 Node.js 16+（推荐 18+）。"
	}
}

func installCLI(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "npx", "@larksuite/cli@latest", "install")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install lark-cli failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
