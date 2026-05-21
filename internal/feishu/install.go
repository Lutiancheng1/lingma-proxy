package feishu

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const (
	minimumNodeMajor     = 16
	recommendedNodeMajor = 18
)

func detectBinary(name string, versionArgs ...string) BinaryStatus {
	path, err := lookPathWithResolvedEnv(name)
	if err != nil {
		return BinaryStatus{Found: false}
	}
	status := BinaryStatus{Found: true, Path: path}
	if len(versionArgs) > 0 {
		cmd := commandWithEnv(path, versionArgs...)
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
	if node.Found {
		major := parseNodeMajor(node.Version)
		switch {
		case major > 0 && major < minimumNodeMajor:
			node.Hint = fmt.Sprintf("飞书 CLI 当前要求 Node.js >=%d，请升级 Node.js；推荐 %d+。", minimumNodeMajor, recommendedNodeMajor)
		case major >= minimumNodeMajor && major < recommendedNodeMajor:
			node.Hint = fmt.Sprintf("Node.js %d 满足飞书 CLI 最低要求，但建议升级到 %d+ 以减少 npm/npx 兼容问题。", major, recommendedNodeMajor)
		}
	}
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
		return fmt.Sprintf("请先安装 Node.js %d+（推荐 %d+），例如通过 https://nodejs.org 或 Homebrew。", minimumNodeMajor, recommendedNodeMajor)
	case "windows":
		return fmt.Sprintf("请先安装 Node.js %d+（推荐 %d+），例如通过 https://nodejs.org 或 winget。", minimumNodeMajor, recommendedNodeMajor)
	default:
		return fmt.Sprintf("请先安装 Node.js %d+（推荐 %d+）。", minimumNodeMajor, recommendedNodeMajor)
	}
}

func installCLI(ctx context.Context) error {
	node, npm, npx := detectNodeAndNPM()
	if !node.Found || !npm.Found || !npx.Found {
		return fmt.Errorf("install lark-cli failed: Node.js/npm/npx prerequisite missing: node=%s npm=%s npx=%s", describeBinary(node), describeBinary(npm), describeBinary(npx))
	}
	if major := parseNodeMajor(node.Version); major > 0 && major < minimumNodeMajor {
		return fmt.Errorf("install lark-cli failed: Node.js version %s is below required >=%d", node.Version, minimumNodeMajor)
	}
	cmd := commandContextWithEnv(ctx, "npx", "@larksuite/cli@latest", "install")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install lark-cli failed: command=%q node=%s npm=%s npx=%s error=%w output=%s", "npx @larksuite/cli@latest install", describeBinary(node), describeBinary(npm), describeBinary(npx), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func parseNodeMajor(version string) int {
	trimmed := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if trimmed == "" {
		return 0
	}
	major, _ := strconv.Atoi(strings.Split(trimmed, ".")[0])
	return major
}

func describeBinary(status BinaryStatus) string {
	if !status.Found {
		return "<missing>"
	}
	parts := []string{}
	if strings.TrimSpace(status.Version) != "" {
		parts = append(parts, strings.TrimSpace(status.Version))
	}
	if strings.TrimSpace(status.Path) != "" {
		parts = append(parts, strings.TrimSpace(status.Path))
	}
	if len(parts) == 0 {
		return "<found>"
	}
	return strings.Join(parts, " @ ")
}
