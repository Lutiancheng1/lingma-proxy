package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	minimumNodeMajor = 20
	minimumNodeMinor = 12
	preferredNodeLTS = "22.11.0"
	fallbackNodeLTS  = "20.18.0"
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
			status.Version = strings.TrimSpace(decodeCommandOutput(output))
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
		return fmt.Sprintf("请先安装 Node.js %s+，例如通过 nvm、fnm、volta、Homebrew 或 https://nodejs.org。", minimumNodeVersionText())
	case "windows":
		return fmt.Sprintf("请先安装 Node.js %s+，例如通过 https://nodejs.org 或 winget。", minimumNodeVersionText())
	default:
		return fmt.Sprintf("请先安装 Node.js %s+。", minimumNodeVersionText())
	}
}

func installCLI(ctx context.Context, onLine func(string)) error {
	emitInstallLine(onLine, "检查 Node.js/npm/npx 环境...")
	node, npm, npx := detectNodeAndNPM()
	if nodeInstallPrerequisiteNeedsRepair(node, npm, npx) {
		nextNode, nextNPM, nextNPX := ensureInstallNodePrerequisite(ctx, node, npm, npx, onLine)
		node, npm, npx = nextNode, nextNPM, nextNPX
	}
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

func nodeInstallPrerequisiteNeedsRepair(node, npm, npx BinaryStatus) bool {
	if !node.Found || !npm.Found || !npx.Found {
		return true
	}
	major, minor := parseNodeVersion(node.Version)
	return !nodeVersionSupported(major, minor)
}

func ensureInstallNodePrerequisite(ctx context.Context, node, npm, npx BinaryStatus, onLine func(string)) (BinaryStatus, BinaryStatus, BinaryStatus) {
	switch runtime.GOOS {
	case "darwin":
		return ensureDarwinInstallNodePrerequisite(ctx, node, npm, npx, onLine)
	case "windows":
		return ensureWindowsInstallNodePrerequisite(ctx, node, npm, npx, onLine)
	default:
		return node, npm, npx
	}
}

func ensureWindowsInstallNodePrerequisite(ctx context.Context, node, npm, npx BinaryStatus, onLine func(string)) (BinaryStatus, BinaryStatus, BinaryStatus) {
	emitInstallLine(onLine, fmt.Sprintf("当前 Node.js 环境不满足要求，尝试在 Windows 上安装或切换 Node.js %s+...", minimumNodeVersionText()))
	for _, step := range windowsNodeInstallSteps() {
		if !step.Available() {
			continue
		}
		emitInstallLine(onLine, step.Label)
		if err := step.Run(ctx, onLine); err != nil {
			emitInstallLine(onLine, fmt.Sprintf("%s 失败，尝试下一个方案：%v", step.Name, err))
			resetResolvedCommandEnv()
			continue
		}
		resetResolvedCommandEnv()
		nextNode, nextNPM, nextNPX := detectNodeAndNPM()
		if !nodeInstallPrerequisiteNeedsRepair(nextNode, nextNPM, nextNPX) {
			emitInstallLine(onLine, "Node.js 环境已就绪："+describeBinary(nextNode))
			return nextNode, nextNPM, nextNPX
		}
		emitInstallLine(onLine, fmt.Sprintf("%s 执行完成，但 Node.js/npm/npx 仍未满足要求，继续尝试下一个方案。", step.Name))
	}
	return node, npm, npx
}

type nodeInstallStep struct {
	Name      string
	Label     string
	Available func() bool
	Run       func(context.Context, func(string)) error
}

func windowsNodeInstallSteps() []nodeInstallStep {
	return []nodeInstallStep{
		{
			Name:  "nvm-windows",
			Label: "检测到 nvm-windows，尝试安装并使用 Node.js LTS...",
			Available: func() bool {
				return windowsNVMPath() != ""
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				nvm := windowsNVMPath()
				if nvm == "" {
					return fmt.Errorf("nvm-windows not found")
				}
				if err := runInstallStep(ctx, []string{nvm, "install", preferredNodeLTS, "64"}, onLine); err != nil {
					emitInstallLine(onLine, fmt.Sprintf("nvm install %s 失败，尝试固定 Node.js %s：%v", preferredNodeLTS, fallbackNodeLTS, err))
					if fallbackErr := runInstallStep(ctx, []string{nvm, "install", fallbackNodeLTS, "64"}, onLine); fallbackErr != nil {
						return fallbackErr
					}
				}
				version, err := latestCompatibleWindowsNVMVersion(ctx, nvm)
				if err != nil || version == "" {
					return fmt.Errorf("resolve installed nvm node version: %w", err)
				}
				emitInstallLine(onLine, "nvm-windows 使用 Node.js "+version)
				return runInstallStep(ctx, []string{nvm, "use", version, "64"}, onLine)
			},
		},
		{
			Name:  "winget",
			Label: "检测到 winget，尝试安装 Node.js LTS...",
			Available: func() bool {
				_, err := lookPathWithResolvedEnv("winget")
				return err == nil
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				return runInstallStep(ctx, []string{"winget", "install", "OpenJS.NodeJS.LTS", "-e", "--silent", "--accept-package-agreements", "--accept-source-agreements"}, onLine)
			},
		},
	}
}

func latestCompatibleWindowsNVMVersion(ctx context.Context, nvm string) (string, error) {
	cmd := commandContextWithEnv(ctx, nvm, "list")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w output=%s", err, truncateInstallOutput(decodeCommandOutput(output)))
	}
	return selectPreferredCompatibleNodeVersion(decodeCommandOutput(output)), nil
}

var nodeVersionPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

func selectPreferredCompatibleNodeVersion(output string) string {
	type candidate struct {
		nodeVersionCandidate
		raw string
	}
	var candidates []candidate
	for _, match := range nodeVersionPattern.FindAllStringSubmatch(output, -1) {
		if len(match) < 2 {
			continue
		}
		parts := strings.Split(match[1], ".")
		if len(parts) != 3 {
			continue
		}
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		patch, _ := strconv.Atoi(parts[2])
		if !nodeVersionSupported(major, minor) {
			continue
		}
		candidates = append(candidates, candidate{nodeVersionCandidate: nodeVersionCandidate{major: major, minor: minor, patch: patch}, raw: match[1]})
	}
	if len(candidates) == 0 {
		return ""
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if preferNodeCandidate(candidate.nodeVersionCandidate, best.nodeVersionCandidate) {
			best = candidate
		}
	}
	return best.raw
}

type nodeVersionCandidate struct {
	major, minor, patch int
}

func preferNodeCandidate(candidate, current nodeVersionCandidate) bool {
	candidateRank := nodeStabilityRank(candidate.major)
	currentRank := nodeStabilityRank(current.major)
	if candidateRank != currentRank {
		return candidateRank < currentRank
	}
	if candidate.major != current.major {
		return candidate.major > current.major
	}
	if candidate.minor != current.minor {
		return candidate.minor > current.minor
	}
	return candidate.patch > current.patch
}

func nodeStabilityRank(major int) int {
	switch major {
	case 22:
		return 0
	case 20:
		return 1
	default:
		if major > 22 {
			return 2
		}
		return 3
	}
}

func windowsNVMPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, candidate := range windowsNVMPathCandidates() {
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	for _, name := range []string{"nvm.exe", "nvm"} {
		if path, err := lookPathWithResolvedEnv(name); err == nil {
			return path
		}
	}
	return ""
}

func windowsNVMPathCandidates() []string {
	home, _ := os.UserHomeDir()
	candidates := []string{}
	addRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		candidates = append(candidates, filepath.Join(root, "nvm.exe"))
	}
	addRoot(os.Getenv("NVM_HOME"))
	if home != "" {
		addRoot(filepath.Join(home, "AppData", "Local", "nvm"))
		addRoot(filepath.Join(home, "AppData", "Roaming", "nvm"))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); strings.TrimSpace(localAppData) != "" {
		addRoot(filepath.Join(localAppData, "nvm"))
	}
	if appData := os.Getenv("APPDATA"); strings.TrimSpace(appData) != "" {
		addRoot(filepath.Join(appData, "nvm"))
	}
	addRoot(`C:\nvm4w`)
	addRoot(`D:\nvm4w`)
	return uniqueStrings(candidates)
}

func ensureDarwinInstallNodePrerequisite(ctx context.Context, node, npm, npx BinaryStatus, onLine func(string)) (BinaryStatus, BinaryStatus, BinaryStatus) {
	emitInstallLine(onLine, fmt.Sprintf("当前 Node.js 环境不满足要求，尝试通过 macOS 版本管理器安装 Node.js %s+...", minimumNodeVersionText()))
	for _, step := range darwinNodeInstallSteps() {
		if !step.Available() {
			continue
		}
		emitInstallLine(onLine, step.Label)
		if err := step.Run(ctx, onLine); err != nil {
			emitInstallLine(onLine, fmt.Sprintf("%s 失败，尝试下一个方案：%v", step.Name, err))
			resetResolvedCommandEnv()
			continue
		}
		resetResolvedCommandEnv()
		nextNode, nextNPM, nextNPX := detectNodeAndNPM()
		if !nodeInstallPrerequisiteNeedsRepair(nextNode, nextNPM, nextNPX) {
			emitInstallLine(onLine, "Node.js 环境已就绪："+describeBinary(nextNode))
			return nextNode, nextNPM, nextNPX
		}
		emitInstallLine(onLine, fmt.Sprintf("%s 执行完成，但 Node.js/npm/npx 仍未满足要求，继续尝试下一个方案。", step.Name))
	}
	return node, npm, npx
}

func darwinNodeInstallSteps() []nodeInstallStep {
	home, _ := os.UserHomeDir()
	nvmScript := filepath.Join(home, ".nvm", "nvm.sh")
	return []nodeInstallStep{
		{
			Name:  "nvm",
			Label: "检测到 nvm，尝试安装并使用 Node.js LTS...",
			Available: func() bool {
				return isExecutableFile(nvmScript) || fileExists(nvmScript)
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				script := `export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"; . "$NVM_DIR/nvm.sh"; nvm install --lts; nvm use --lts; node --version; npm --version; npx --version`
				return runShellInstallStep(ctx, "/bin/zsh", script, onLine)
			},
		},
		{
			Name:  "fnm",
			Label: "检测到 fnm，尝试安装 Node.js LTS...",
			Available: func() bool {
				_, err := lookPathWithResolvedEnv("fnm")
				return err == nil
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				return runInstallStep(ctx, []string{"fnm", "install", "--lts"}, onLine)
			},
		},
		{
			Name:  "volta",
			Label: "检测到 volta，尝试安装 Node.js LTS...",
			Available: func() bool {
				_, err := lookPathWithResolvedEnv("volta")
				return err == nil
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				return runInstallStep(ctx, []string{"volta", "install", "node"}, onLine)
			},
		},
		{
			Name:  "Homebrew",
			Label: "检测到 Homebrew，尝试安装 Node.js...",
			Available: func() bool {
				_, err := lookPathWithResolvedEnv("brew")
				return err == nil
			},
			Run: func(ctx context.Context, onLine func(string)) error {
				return runInstallStep(ctx, []string{"brew", "install", "node"}, onLine)
			},
		},
	}
}

func runShellInstallStep(ctx context.Context, shell, script string, onLine func(string)) error {
	cmd := commandContextWithEnv(ctx, shell, "-lc", script)
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
	// Use the official well-known endpoint from
	// https://open.feishu.cn/document/no_class/mcp-archive/feishu-cli-installation-guide.md
	// so we install whatever the manifest currently lists (26 skills today,
	// growing). The legacy `larksuite/cli` alias stays as a fallback for older
	// skills CLIs that don't accept a URL source.
	return [][]string{
		{"npx", "-y", "skills@1.5.6", "add", "https://open.feishu.cn", "--skill", "-y", "-g"},
		{"npx", "-y", "skills@1.5.5", "add", "https://open.feishu.cn", "--skill", "-y", "-g"},
		{"npx", "-y", "skills", "add", "https://open.feishu.cn", "--skill", "-y", "-g"},
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
	for _, args := range [][]string{{"config", "get", "prefix"}, {"config", "get", "cache"}} {
		dir := npmConfigDir(args...)
		if dir == "" || dir == "undefined" || strings.EqualFold(dir, "null") {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("install lark-cli failed: create npm configured dir %s: %w", dir, err)
		}
	}
	return nil
}

func npmConfigDir(args ...string) string {
	cmd := commandWithEnv("npm", args...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decodeCommandOutput(output))
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
