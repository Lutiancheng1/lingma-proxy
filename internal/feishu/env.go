package feishu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var (
	commandEnvOnce sync.Once
	commandEnv     []string
	commandPath    string
)

func resolvedCommandEnv() []string {
	commandEnvOnce.Do(func() {
		commandPath = resolvedPATH()
		base := os.Environ()
		next := make([]string, 0, len(base)+1)
		pathWritten := false
		for _, item := range base {
			if isPATHEnvItem(item) {
				next = append(next, "PATH="+commandPath)
				pathWritten = true
				continue
			}
			next = append(next, item)
		}
		if !pathWritten {
			next = append(next, "PATH="+commandPath)
		}
		commandEnv = next
	})
	return append([]string(nil), commandEnv...)
}

func resetResolvedCommandEnv() {
	commandEnvOnce = sync.Once{}
	commandEnv = nil
	commandPath = ""
}

func resolvedPATH() string {
	var segments []string
	segments = append(segments, pathSegments(os.Getenv("PATH"))...)
	segments = append(segments, pathSegments(loginShellPATH())...)
	segments = append(segments, nodeVersionManagerPATHSegments()...)
	switch runtime.GOOS {
	case "darwin":
		segments = append(segments, "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	case "linux":
		segments = append(segments, "/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	case "windows":
		segments = append(segments, pathSegments(os.Getenv("Path"))...)
		segments = append(segments, windowsNodePATHSegments()...)
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			segments = append(segments, filepath.Join(appData, "npm"))
		}
	}
	segments = append(segments, npmGlobalPATHSegments(segments)...)
	segments = uniqueStrings(segments)
	if selected := selectSupportedNodeBinDir(segments); selected != "" {
		segments = placeSelectedNodeDir(segments, selected)
	}
	return strings.Join(segments, string(os.PathListSeparator))
}

func windowsNodePATHSegments() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	segments := make([]string, 0, 4)
	if nvmSymlink := strings.TrimSpace(os.Getenv("NVM_SYMLINK")); nvmSymlink != "" {
		segments = append(segments, nvmSymlink)
	}
	for _, envName := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		root := strings.TrimSpace(os.Getenv(envName))
		if root == "" {
			continue
		}
		if envName == "LOCALAPPDATA" {
			segments = append(segments, filepath.Join(root, "Programs", "nodejs"))
			continue
		}
		segments = append(segments, filepath.Join(root, "nodejs"))
	}
	return segments
}

func npmGlobalPATHSegments(baseSegments []string) []string {
	prefixes := npmGlobalPrefixes(baseSegments)
	out := make([]string, 0, len(prefixes)*2)
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			out = append(out, prefix)
			continue
		}
		out = append(out, filepath.Join(prefix, "bin"))
	}
	return out
}

func npmGlobalPrefixes(baseSegments []string) []string {
	var prefixes []string
	if prefix := strings.TrimSpace(os.Getenv("npm_config_prefix")); prefix != "" {
		prefixes = append(prefixes, prefix)
	}
	if os.Getenv("FEISHU_SKIP_NPM_PREFIX_PROBE") == "1" {
		return uniqueStrings(prefixes)
	}
	npmPath, err := lookPathInSegments("npm", baseSegments)
	if err != nil {
		return uniqueStrings(prefixes)
	}
	for _, args := range [][]string{{"config", "get", "prefix"}, {"prefix", "-g"}} {
		if prefix := npmPrefixFromCommand(npmPath, baseSegments, args...); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	return uniqueStrings(prefixes)
}

func npmPrefixFromCommand(npmPath string, baseSegments []string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, npmPath, args...)
	cmd.Env = commandEnvForPATH(strings.Join(baseSegments, string(os.PathListSeparator)))
	applyCommandPlatformOptions(cmd)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decodeCommandOutput(output))
}

func nodeVersionManagerPATHSegments() []string {
	home, _ := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		return nil
	}
	var roots []string
	switch runtime.GOOS {
	case "darwin", "linux":
		roots = append(roots,
			filepath.Join(home, ".nvm", "versions", "node"),
			filepath.Join(home, ".fnm", "node-versions"),
			filepath.Join(home, ".volta", "tools", "image", "node"),
		)
	case "windows":
		for _, root := range []string{
			filepath.Join(home, "AppData", "Roaming", "nvm"),
			filepath.Join(home, "AppData", "Local", "nvm"),
			filepath.Join(home, "AppData", "Local", "fnm_multishells"),
			filepath.Join(home, "AppData", "Local", "fnm", "node-versions"),
			filepath.Join(home, "AppData", "Local", "Volta", "tools", "image", "node"),
		} {
			roots = append(roots, root)
		}
	}
	return nodeVersionDirs(roots)
}

func nodeVersionDirs(roots []string) []string {
	var dirs []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if runtime.GOOS != "windows" {
				dir = filepath.Join(dir, "bin")
			}
			if isExecutableFile(filepath.Join(dir, "node")) || isExecutableFile(filepath.Join(dir, "node.exe")) {
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

func selectSupportedNodeBinDir(segments []string) string {
	type candidate struct {
		dir          string
		major, minor int
	}
	var candidates []candidate
	for _, dir := range segments {
		nodePath := filepath.Join(dir, "node")
		if runtime.GOOS == "windows" {
			nodePath = filepath.Join(dir, "node.exe")
		}
		if !isExecutableFile(nodePath) {
			continue
		}
		version := nodeVersionAtPath(nodePath)
		major, minor := parseNodeVersion(version)
		if !nodeVersionSupported(major, minor) {
			continue
		}
		candidates = append(candidates, candidate{dir: dir, major: major, minor: minor})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].major != candidates[j].major {
			return candidates[i].major > candidates[j].major
		}
		return candidates[i].minor > candidates[j].minor
	})
	return candidates[0].dir
}

func nodeVersionAtPath(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	applyCommandPlatformOptions(cmd)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decodeCommandOutput(output))
}

func loginShellPATH() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		switch runtime.GOOS {
		case "darwin":
			shell = "/bin/zsh"
		case "linux":
			shell = "/bin/bash"
		default:
			return ""
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lc", "printf %s \"$PATH\"")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decodeCommandOutput(output))
}

func pathSegments(pathValue string) []string {
	if strings.TrimSpace(pathValue) == "" {
		return nil
	}
	return strings.Split(pathValue, string(os.PathListSeparator))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func placeSelectedNodeDir(segments []string, selected string) []string {
	segments = removeString(segments, selected)
	insertAt := 0
	for i, dir := range segments {
		if dirHasNodeBinary(dir) {
			insertAt = i
			break
		}
		insertAt = i + 1
	}
	out := make([]string, 0, len(segments)+1)
	out = append(out, segments[:insertAt]...)
	out = append(out, selected)
	out = append(out, segments[insertAt:]...)
	return out
}

func removeString(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func dirHasNodeBinary(dir string) bool {
	if runtime.GOOS == "windows" {
		return isExecutableFile(filepath.Join(dir, "node.exe"))
	}
	return isExecutableFile(filepath.Join(dir, "node"))
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0111 != 0
}

func lookPathWithResolvedEnv(name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		if isExecutableFile(name) {
			return name, nil
		}
		return "", exec.ErrNotFound
	}
	return lookPathInSegments(name, pathSegments(resolvedPATH()))
}

func lookPathInSegments(name string, segments []string) (string, error) {
	for _, dir := range segments {
		candidate := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			for _, ext := range []string{".exe", ".cmd", ".bat"} {
				if isExecutableFile(candidate + ext) {
					return candidate + ext, nil
				}
			}
		}
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func commandEnvForPATH(pathValue string) []string {
	base := os.Environ()
	next := make([]string, 0, len(base)+1)
	pathWritten := false
	for _, item := range base {
		if isPATHEnvItem(item) {
			next = append(next, "PATH="+pathValue)
			pathWritten = true
			continue
		}
		next = append(next, item)
	}
	if !pathWritten {
		next = append(next, "PATH="+pathValue)
	}
	return next
}

func isPATHEnvItem(item string) bool {
	key, _, ok := strings.Cut(item, "=")
	return ok && strings.EqualFold(key, "PATH")
}

func commandWithEnv(name string, args ...string) *exec.Cmd {
	executable := resolveCommandName(name)
	executable = unquoteWindowsExecutable(executable)
	cmd := exec.Command(executable, args...)
	if shell, shellArgs, shellCmdLine, ok := windowsShellCommand(executable, args); ok {
		cmd = exec.Command(shell, shellArgs...)
		applyWindowsRawCommandLine(cmd, shellCmdLine)
	}
	cmd.Env = resolvedCommandEnv()
	applyCommandPlatformOptions(cmd)
	return cmd
}

func commandContextWithEnv(ctx context.Context, name string, args ...string) *exec.Cmd {
	executable := resolveCommandName(name)
	executable = unquoteWindowsExecutable(executable)
	cmd := exec.CommandContext(ctx, executable, args...)
	if shell, shellArgs, shellCmdLine, ok := windowsShellCommand(executable, args); ok {
		cmd = exec.CommandContext(ctx, shell, shellArgs...)
		applyWindowsRawCommandLine(cmd, shellCmdLine)
	}
	cmd.Env = resolvedCommandEnv()
	applyCommandPlatformOptions(cmd)
	return cmd
}

func decodeCommandOutput(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	if utf8.Valid(output) {
		return string(output)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return string(output)
}

func windowsShellCommand(executable string, args []string) (string, []string, string, bool) {
	if runtime.GOOS != "windows" {
		return "", nil, "", false
	}
	executable = unquoteWindowsExecutable(executable)
	ext := strings.ToLower(filepath.Ext(executable))
	if ext != ".cmd" && ext != ".bat" {
		return "", nil, "", false
	}
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, "call", windowsCmdQuote(executable))
	for _, arg := range args {
		parts = append(parts, windowsCmdQuote(arg))
	}
	command := strings.Join(parts, " ")
	shellArgs := []string{"/D", "/C", command}
	shellCmdLine := `cmd.exe /D /C ` + command
	return "cmd.exe", shellArgs, shellCmdLine, true
}

func windowsCmdQuote(value string) string {
	if value == "" {
		return `""`
	}
	escaped := strings.ReplaceAll(value, `"`, `""`)
	return `"` + escaped + `"`
}

func unquoteWindowsExecutable(value string) string {
	if runtime.GOOS != "windows" {
		return value
	}
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func resolveCommandName(name string) string {
	if resolved, err := lookPathWithResolvedEnv(name); err == nil {
		return resolved
	}
	return name
}
