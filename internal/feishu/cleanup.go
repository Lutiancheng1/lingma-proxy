package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type CleanupOptions struct {
	IncludeImportedSkills bool
}

var (
	uninstallLarkCLIFunc = uninstallLarkCLI
	npmShimDirsFunc      = npmShimDirs
)

func (m *Manager) CleanupArtifacts(ctx context.Context, opts CleanupOptions) ([]string, error) {
	_ = m.Stop()

	m.setCleanupOutput("正在清理飞书 CLI、Skills 和授权信息...", "")

	results, err := cleanupBridgeArtifacts(ctx, m.setCleanupOutput)
	if opts.IncludeImportedSkills {
		m.setCleanupOutput("正在清理用户导入 Bridge Skills...", "")
		if skillResults, skillErr := m.cleanupImportedSkills(ctx); skillErr != nil {
			if err != nil {
				err = fmt.Errorf("%v; %w", err, skillErr)
			} else {
				err = skillErr
			}
		} else {
			results = append(results, skillResults...)
		}
	}
	m.refreshStatus(ctx)

	summary := strings.Join(results, "；")
	if summary == "" {
		summary = "未发现需要清理的飞书 CLI/Skills/授权信息"
	}
	if err != nil {
		m.setCleanupOutput(summary, err.Error())
	} else {
		m.setCleanupOutput(summary, "")
	}

	if err != nil {
		m.logf("error", "飞书 Bridge 清理失败："+err.Error())
		return results, err
	}
	m.logf("info", "飞书 Bridge 清理完成："+summary)
	return results, nil
}

func (m *Manager) setCleanupOutput(output string, errText string) {
	m.mu.Lock()
	m.status.LastOutput = output
	m.status.LastError = errText
	status := m.status
	m.mu.Unlock()
	m.emit(status)
}

func (m *Manager) cleanupImportedSkills(ctx context.Context) ([]string, error) {
	if m.skillService == nil {
		return nil, nil
	}
	removed, err := m.skillService.ClearAll(ctx)
	if removed == 0 && err == nil {
		return []string{"未发现用户导入 Bridge Skills"}, nil
	}
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.status.SkillCount = 0
	m.mu.Unlock()
	m.notifyConversationChanged()
	return []string{fmt.Sprintf("已移除用户导入 Bridge Skills %d 个", removed)}, nil
}

func cleanupBridgeArtifacts(ctx context.Context, onProgress func(string, string)) ([]string, error) {
	var results []string
	var failures []string

	if onProgress != nil {
		onProgress("正在卸载全局 @larksuite/cli...", "")
	}
	if err := uninstallLarkCLIFunc(ctx); err != nil {
		failures = append(failures, err.Error())
	} else {
		results = append(results, "已尝试卸载 @larksuite/cli")
	}

	if onProgress != nil {
		onProgress("正在移除 lark-cli 启动文件...", "")
	}
	removedShims := removeLarkCLIShims()
	if removedShims > 0 {
		results = append(results, fmt.Sprintf("已移除 lark-cli 启动文件 %d 个", removedShims))
	}

	if onProgress != nil {
		onProgress("正在移除官方 lark-* Skills...", "")
	}
	removedSkills, err := removeOfficialLarkSkills()
	if err != nil {
		failures = append(failures, err.Error())
	}
	if removedSkills > 0 {
		results = append(results, fmt.Sprintf("已移除官方 lark-* Skills %d 个", removedSkills))
	}

	if onProgress != nil {
		onProgress("正在清理 ~/.lark-cli 配置、授权和事件缓存...", "")
	}
	if err := removeLarkCLIState(); err != nil {
		failures = append(failures, err.Error())
	} else {
		results = append(results, "已清理 ~/.lark-cli 配置、授权和事件缓存")
	}

	if len(failures) > 0 {
		return results, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return results, nil
}

func uninstallLarkCLI(ctx context.Context) error {
	npm := detectBinary("npm", "--version")
	if !npm.Found {
		return nil
	}
	cmd := commandContextWithEnv(ctx, "npm", "uninstall", "-g", "@larksuite/cli")
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(decodeCommandOutput(output))
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("卸载 @larksuite/cli 失败：%s", text)
	}
	return nil
}

func removeLarkCLIShims() int {
	seen := map[string]struct{}{}
	count := 0
	for _, dir := range npmShimDirsFunc() {
		for _, name := range larkCLIShimNames() {
			path := filepath.Join(dir, name)
			key := strings.ToLower(path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if err := os.Remove(path); err == nil {
				count++
			}
		}
	}
	return count
}

func npmShimDirs() []string {
	var dirs []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		dirs = append(dirs, filepath.Clean(path))
	}
	if prefix := npmConfigDir("config", "get", "prefix"); prefix != "" && prefix != "undefined" {
		add(prefix)
		if runtime.GOOS != "windows" {
			add(filepath.Join(prefix, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			add(filepath.Join(home, "AppData", "Roaming", "npm"))
		} else {
			add(filepath.Join(home, ".npm-global", "bin"))
		}
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		add(filepath.Join(appData, "npm"))
	}
	return dirs
}

func larkCLIShimNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"lark-cli", "lark-cli.cmd", "lark-cli.ps1"}
	}
	return []string{"lark-cli"}
}

func removeOfficialLarkSkills() (int, error) {
	removed := 0
	for _, root := range globalSkillsRoots {
		rootPath := expandHome(root)
		entries, err := os.ReadDir(rootPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || !strings.HasPrefix(name, "lark-") {
				continue
			}
			if err := os.RemoveAll(filepath.Join(rootPath, name)); err != nil {
				return removed, fmt.Errorf("移除 Skill %s 失败：%w", name, err)
			}
			removed++
		}
	}
	if err := rewriteSkillLocksWithoutLark(); err != nil {
		return removed, err
	}
	return removed, nil
}

func rewriteSkillLocksWithoutLark() error {
	for _, lockPath := range skillLockPaths {
		path := expandHome(lockPath)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			return fmt.Errorf("解析 Skill lock 失败：%w", err)
		}
		rawSkills, ok := body["skills"].(map[string]any)
		if !ok {
			continue
		}
		changed := false
		for name := range rawSkills {
			if strings.HasPrefix(name, "lark-") {
				delete(rawSkills, name)
				changed = true
			}
		}
		if !changed {
			continue
		}
		next, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return fmt.Errorf("写入 Skill lock 失败：%w", err)
		}
		next = append(next, '\n')
		if err := os.WriteFile(path, next, 0o644); err != nil {
			return fmt.Errorf("更新 Skill lock 失败：%w", err)
		}
	}
	return nil
}

func removeLarkCLIState() error {
	path, err := larkConfigPath()
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("清理 %s 失败：%w", root, err)
	}
	return nil
}
