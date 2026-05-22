package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	minimumNodeMajor = 20
	minimumNodeMinor = 12
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
		major, minor := parseNodeVersion(node.Version)
		switch {
		case !nodeVersionSupported(major, minor):
			node.Hint = fmt.Sprintf("飞书 CLI 当前安装链路要求 Node.js >=%s，请升级 Node.js 后重试。", minimumNodeVersionText())
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
		return fmt.Sprintf("请先安装 Node.js %s+，例如通过 https://nodejs.org 或 Homebrew。", minimumNodeVersionText())
	case "windows":
		return fmt.Sprintf("请先安装 Node.js %s+，例如通过 https://nodejs.org 或 winget。", minimumNodeVersionText())
	default:
		return fmt.Sprintf("请先安装 Node.js %s+。", minimumNodeVersionText())
	}
}

func installCLI(ctx context.Context, onLine func(string)) error {
	emitInstallLine(onLine, "检查 Node.js/npm/npx 环境...")
	node, npm, npx := detectNodeAndNPM()
	if !node.Found || !npm.Found || !npx.Found {
		return fmt.Errorf("install lark-cli failed: Node.js/npm/npx prerequisite missing: node=%s npm=%s npx=%s", describeBinary(node), describeBinary(npm), describeBinary(npx))
	}
	if major, minor := parseNodeVersion(node.Version); !nodeVersionSupported(major, minor) {
		return fmt.Errorf("install lark-cli failed: Node.js version %s is below required >=%s", node.Version, minimumNodeVersionText())
	}
	if err := ensureNPMGlobalDir(); err != nil {
		return err
	}
	emitInstallLine(onLine, "安装飞书 CLI: npm install -g @larksuite/cli")
	if err := runInstallStep(ctx, []string{"npm", "install", "-g", "@larksuite/cli"}, onLine); err != nil {
		resetResolvedCommandEnv()
		if cli := detectBinary("lark-cli", "--version"); cli.Found {
			emitInstallLine(onLine, "飞书 CLI 安装命令返回错误，但 lark-cli 已可用，继续安装 Skills："+describeBinary(cli))
		} else {
			return fmt.Errorf("install lark-cli failed: command=%q node=%s npm=%s npx=%s error=%w", "npm install -g @larksuite/cli", describeBinary(node), describeBinary(npm), describeBinary(npx), err)
		}
	}
	resetResolvedCommandEnv()
	if err := installSkills(ctx, onLine); err != nil {
		return fmt.Errorf("install lark-cli skills failed: node=%s npm=%s npx=%s error=%w", describeBinary(node), describeBinary(npm), describeBinary(npx), err)
	}
	emitInstallLine(onLine, "验证 lark-cli 安装结果...")
	resetResolvedCommandEnv()
	cli := detectBinary("lark-cli", "--version")
	if !cli.Found {
		return fmt.Errorf("install lark-cli failed: install command completed but lark-cli is still missing in resolved PATH")
	}
	emitInstallLine(onLine, "飞书 CLI 安装完成："+describeBinary(cli))
	return nil
}

func installSkills(ctx context.Context, onLine func(string)) error {
	commands := skillsInstallCommands()
	var lastErr error
	for i, argv := range commands {
		emitInstallLine(onLine, "安装飞书 CLI Skills: "+strings.Join(argv, " "))
		if err := runInstallStep(ctx, argv, onLine); err != nil {
			lastErr = err
			if i < len(commands)-1 {
				emitInstallLine(onLine, fmt.Sprintf("Skills 安装命令失败，尝试兼容版本重试：%v", err))
				continue
			}
			break
		}
		return nil
	}
	return fmt.Errorf("all skills install commands failed: %w", lastErr)
}

func skillsInstallCommands() [][]string {
	return [][]string{
		{"npx", "-y", "skills@1.5.6", "add", "larksuite/cli", "-y", "-g"},
		{"npx", "-y", "skills@1.5.5", "add", "larksuite/cli", "-y", "-g"},
		{"npx", "-y", "skills", "add", "larksuite/cli", "-y", "-g"},
	}
}

func runInstallStep(ctx context.Context, argv []string, onLine func(string)) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := commandContextWithEnv(ctx, argv[0], argv[1:]...)
	var lines []string
	err := runStreamingCommand(ctx, cmd, func(line string) {
		line = formatOutputLine(line)
		if line == "" {
			return
		}
		lines = append(lines, line)
		emitInstallLine(onLine, line)
	}, nil)
	if err != nil {
		return fmt.Errorf("%w output=%s", err, truncateInstallOutput(strings.Join(lines, "\n")))
	}
	return nil
}

func emitInstallLine(onLine func(string), line string) {
	line = strings.TrimSpace(line)
	if line != "" && onLine != nil {
		onLine(line)
	}
}

func truncateInstallOutput(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 4000 {
		return text
	}
	return text[:4000] + "...(truncated)"
}

func ensureNPMGlobalDir() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	appData := strings.TrimSpace(os.Getenv("APPDATA"))
	if appData == "" {
		return nil
	}
	npmDir := filepath.Join(appData, "npm")
	if err := os.MkdirAll(npmDir, 0755); err != nil {
		return fmt.Errorf("install lark-cli failed: create npm global bin dir %s: %w", npmDir, err)
	}
	return nil
}

func parseNodeMajor(version string) int {
	major, _ := parseNodeVersion(version)
	return major
}

func parseNodeVersion(version string) (int, int) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if trimmed == "" {
		return 0, 0
	}
	parts := strings.Split(trimmed, ".")
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

func nodeVersionSupported(major, minor int) bool {
	if major == 0 {
		return true
	}
	return major > minimumNodeMajor || major == minimumNodeMajor && minor >= minimumNodeMinor
}

func minimumNodeVersionText() string {
	return fmt.Sprintf("%d.%d", minimumNodeMajor, minimumNodeMinor)
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
