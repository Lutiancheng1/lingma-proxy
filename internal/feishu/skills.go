package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// feishuSkillManifestURL is the official well-known endpoint that lists every
// skill the feishu CLI expects. Keeping the required set in sync with this
// manifest prevents the onboarding step from reporting "缺失或未完整安装" once
// the feishu side adds a new skill.
const feishuSkillManifestURL = "https://open.feishu.cn/.well-known/skills/index.json"

// fallbackRequiredSkillNames is a snapshot of the manifest taken on
// 2026-05-23. It is only consulted when the live manifest cannot be fetched
// (e.g. the user is offline). Add new entries here whenever the live manifest
// expands so cached/offline runs stay accurate.
var fallbackRequiredSkillNames = []string{
	"lark-approval",
	"lark-apps",
	"lark-attendance",
	"lark-base",
	"lark-calendar",
	"lark-contact",
	"lark-doc",
	"lark-drive",
	"lark-event",
	"lark-im",
	"lark-mail",
	"lark-markdown",
	"lark-minutes",
	"lark-okr",
	"lark-openapi-explorer",
	"lark-shared",
	"lark-sheets",
	"lark-skill-maker",
	"lark-slides",
	"lark-task",
	"lark-vc",
	"lark-vc-agent",
	"lark-whiteboard",
	"lark-wiki",
	"lark-workflow-meeting-summary",
	"lark-workflow-standup-report",
}

type skillManifestEntry struct {
	Name string `json:"name"`
}

type skillManifest struct {
	Skills []skillManifestEntry `json:"skills"`
}

// requiredSkillNamesFn is overridable in tests so we can avoid hitting the
// network from unit tests.
var requiredSkillNamesFn = resolveRequiredSkillNames

var (
	manifestCacheMu      sync.Mutex
	manifestCacheNames   []string
	manifestCacheFetched time.Time
)

const manifestCacheTTL = 6 * time.Hour

func resolveRequiredSkillNames(ctx context.Context) []string {
	manifestCacheMu.Lock()
	if len(manifestCacheNames) > 0 && time.Since(manifestCacheFetched) < manifestCacheTTL {
		cached := append([]string(nil), manifestCacheNames...)
		manifestCacheMu.Unlock()
		return cached
	}
	manifestCacheMu.Unlock()

	names, err := fetchFeishuSkillManifest(ctx)
	if err != nil || len(names) == 0 {
		return append([]string(nil), fallbackRequiredSkillNames...)
	}

	manifestCacheMu.Lock()
	manifestCacheNames = append([]string(nil), names...)
	manifestCacheFetched = time.Now()
	manifestCacheMu.Unlock()
	return names
}

func clearSkillManifestCache() {
	manifestCacheMu.Lock()
	defer manifestCacheMu.Unlock()
	manifestCacheNames = nil
	manifestCacheFetched = time.Time{}
}

func fetchFeishuSkillManifest(ctx context.Context) ([]string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, feishuSkillManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("manifest http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	var manifest skillManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(manifest.Skills))
	names := make([]string, 0, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// discoverSkillsTimeout is kept as a fallback ceiling for the optional npx
// probe used in tests. Production discovery scans the filesystem directly
// (~/.agents/skills/<name>/SKILL.md) and never shells out to npx — the npx
// path measured at ~19s on a real machine, blowing past any reasonable poll
// interval and starving the npm cache lock.
const discoverSkillsTimeout = 12 * time.Second

// globalSkillsRoots are the directories where the `skills` tool installs
// global skills as fallback when the lock file is unavailable. The first
// hit wins for each skill name.
var globalSkillsRoots = []string{
	"~/.agents/skills",
	"~/.hermes/skills",
}

// skillLockPaths are candidate locations for the `skills` tool's lockfile.
// The lockfile is the authoritative record of what `skills add -g` has
// installed and survives even if the user moves the agent directories
// around. We check it first; FS scan is the fallback.
var skillLockPaths = []string{
	"~/.agents/.skill-lock.json",
	"~/.hermes/.skill-lock.json",
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

type skillLockEntry struct {
	SkillPath string `json:"skillPath"`
}

type skillLockFile struct {
	Skills map[string]skillLockEntry `json:"skills"`
}

// readInstalledSkillsFromLock returns the set of skills the `skills` tool
// believes are installed globally. Returns nil if no lock file is present
// or readable — callers fall back to the FS scan.
func readInstalledSkillsFromLock() map[string]struct{} {
	for _, lockPath := range skillLockPaths {
		data, err := os.ReadFile(expandHome(lockPath))
		if err != nil {
			continue
		}
		var lock skillLockFile
		if err := json.Unmarshal(data, &lock); err != nil {
			continue
		}
		if len(lock.Skills) == 0 {
			continue
		}
		out := make(map[string]struct{}, len(lock.Skills))
		for name := range lock.Skills {
			out[name] = struct{}{}
		}
		return out
	}
	return nil
}

// findSkillOnDisk returns the first global skill directory containing
// SKILL.md for the given name, or "" if none.
func findSkillOnDisk(name string) string {
	for _, root := range globalSkillsRoots {
		candidate := filepath.Join(expandHome(root), name)
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		skillFile := filepath.Join(candidate, "SKILL.md")
		fi, err := os.Stat(skillFile)
		if err != nil || fi.IsDir() {
			continue
		}
		realPath, evalErr := filepath.EvalSymlinks(candidate)
		if evalErr != nil {
			realPath = candidate
		}
		return realPath
	}
	return ""
}

func discoverSkills(ctx context.Context) ([]SkillStatus, error) {
	required := requiredSkillNamesFn(ctx)
	lockSet := readInstalledSkillsFromLock()
	statuses := make([]SkillStatus, 0, len(required))
	for _, name := range required {
		status := SkillStatus{Name: name}
		diskPath := findSkillOnDisk(name)
		_, inLock := lockSet[name]

		switch {
		case inLock && diskPath != "":
			status.Found = true
			status.Path = diskPath
		case inLock && diskPath == "":
			// Lock says installed but SKILL.md missing — broken install.
			status.Message = "SKILL.md 缺失（lock 记录已安装）"
		case !inLock && diskPath != "":
			// Disk has it but lock doesn't list it — accept it (user may
			// have installed via a different agent dir). Treat as Found
			// since the runtime cares about the file, not the lock.
			status.Found = true
			status.Path = diskPath
		default:
			status.Message = "未在 skill-lock 与全局 skills 目录中发现"
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func skillsReady(skills []SkillStatus) bool {
	if len(skills) == 0 {
		return false
	}
	for _, skill := range skills {
		if !skill.Found {
			return false
		}
	}
	return true
}

func missingSkillNames(skills []SkillStatus, limit int) []string {
	if limit <= 0 {
		limit = len(skills)
	}
	missing := make([]string, 0)
	for _, skill := range skills {
		if skill.Found {
			continue
		}
		if len(missing) >= limit {
			remaining := 0
			for _, rest := range skills {
				if !rest.Found {
					remaining++
				}
			}
			missing = append(missing, fmt.Sprintf("等另外 %d 个", remaining-len(missing)))
			break
		}
		missing = append(missing, skill.Name)
	}
	return missing
}

func renderLarkSkillView(name string) (string, error) {
	status, err := resolveLarkSkillStatus(name, nil)
	if err != nil {
		return "", err
	}
	text, err := readLarkSkillText(status)
	if err != nil {
		return "", err
	}
	return formatLarkSkillGuide(status, text, 12000), nil
}

func buildRelevantLarkSkillContext(skills []SkillStatus, userText string) string {
	names := inferLarkSkillNames(userText)
	if len(names) == 0 {
		return ""
	}
	sections := make([]string, 0, len(names))
	for _, name := range names {
		status, err := resolveLarkSkillStatus(name, skills)
		if err != nil || !status.Found {
			continue
		}
		text, err := readLarkSkillText(status)
		if err != nil {
			continue
		}
		sections = append(sections, formatLarkSkillGuide(status, text, 5200))
		if len(sections) >= 3 {
			break
		}
	}
	if len(sections) == 0 {
		return ""
	}
	return "本轮任务已自动加载相关官方 lark-cli Skill。执行飞书工具前必须优先遵循以下文档；如果仍不确定，调用 lark_skill_view 读取完整 Skill，不要猜命令或参数。\n\n" + strings.Join(sections, "\n\n---\n\n")
}

func resolveLarkSkillStatus(name string, statuses []SkillStatus) (SkillStatus, error) {
	skillName := normalizeLarkSkillName(name)
	if skillName == "" {
		return SkillStatus{}, fmt.Errorf("skill 名称不能为空")
	}
	if !strings.HasPrefix(skillName, "lark-") {
		return SkillStatus{}, fmt.Errorf("不支持的官方飞书 Skill：%s", name)
	}
	for _, status := range statuses {
		if strings.EqualFold(status.Name, skillName) {
			if status.Found && strings.TrimSpace(status.Path) != "" {
				return status, nil
			}
			break
		}
	}
	if path := findSkillOnDisk(skillName); path != "" {
		return SkillStatus{Name: skillName, Found: true, Path: path}, nil
	}
	return SkillStatus{}, fmt.Errorf("未找到官方飞书 Skill `%s`。请先在 Feishu Bridge 设置页安装 CLI 与 Skills，或执行 /reload-skills 后重试。", skillName)
}

func normalizeLarkSkillName(name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimPrefix(value, "/")
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.TrimPrefix(value, "lark-cli ")
	value = strings.TrimPrefix(value, "lark ")
	switch value {
	case "doc", "docs", "document", "documents", "云文档", "文档":
		return "lark-doc"
	case "sheet", "sheets", "spreadsheet", "spreadsheets", "电子表格", "表格":
		return "lark-sheets"
	case "drive", "file", "files", "folder", "folders", "云盘", "文件", "文件夹":
		return "lark-drive"
	case "im", "message", "messages", "chat", "chats", "消息", "群", "群聊":
		return "lark-im"
	case "calendar", "cal", "日历", "日程", "会议":
		return "lark-calendar"
	case "base", "bitable", "多维表格":
		return "lark-base"
	case "wiki", "知识库":
		return "lark-wiki"
	case "task", "tasks", "任务", "待办":
		return "lark-task"
	case "mail", "email", "邮箱", "邮件":
		return "lark-mail"
	case "contact", "contacts", "通讯录", "联系人":
		return "lark-contact"
	case "minutes", "妙记", "会议纪要":
		return "lark-minutes"
	case "slides", "slide", "幻灯片", "演示文稿":
		return "lark-slides"
	case "approval", "审批":
		return "lark-approval"
	case "attendance", "考勤":
		return "lark-attendance"
	case "okr":
		return "lark-okr"
	case "vc", "video", "视频会议":
		return "lark-vc"
	case "whiteboard", "白板":
		return "lark-whiteboard"
	}
	if strings.HasPrefix(value, "lark-") {
		return value
	}
	return ""
}

func inferLarkSkillNames(text string) []string {
	value := strings.ToLower(strings.TrimSpace(text))
	if value == "" {
		return nil
	}
	type cue struct {
		name  string
		words []string
	}
	cues := []cue{
		{"lark-sheets", []string{"电子表格", "表格", "sheet", "spreadsheet", "单元格", "sheet_id"}},
		{"lark-doc", []string{"云文档", "文档", "docx", "docs", "doc_token", "读取[飞书云文档"}},
		{"lark-drive", []string{"云盘", "云空间", "文件", "文件夹", "drive"}},
		{"lark-im", []string{"消息", "群聊", "群", "im", "chat", "message"}},
		{"lark-calendar", []string{"日历", "日程", "会议", "calendar", "agenda"}},
		{"lark-base", []string{"多维表格", "bitable", "base"}},
		{"lark-wiki", []string{"知识库", "wiki"}},
		{"lark-task", []string{"任务", "待办", "task"}},
		{"lark-mail", []string{"邮箱", "邮件", "mail", "email"}},
		{"lark-contact", []string{"通讯录", "联系人", "contact"}},
		{"lark-minutes", []string{"妙记", "会议纪要", "minutes"}},
		{"lark-slides", []string{"幻灯片", "演示文稿", "slides"}},
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 3)
	for _, item := range cues {
		for _, word := range item.words {
			if strings.Contains(value, strings.ToLower(word)) {
				if _, ok := seen[item.name]; !ok {
					seen[item.name] = struct{}{}
					out = append(out, item.name)
				}
				break
			}
		}
	}
	return out
}

func readLarkSkillText(status SkillStatus) (string, error) {
	path := filepath.Join(status.Path, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败：%w", path, err)
	}
	return string(data), nil
}

func formatLarkSkillGuide(status SkillStatus, text string, limit int) string {
	body := stripYAMLFrontmatter(text)
	body = strings.TrimSpace(body)
	title := status.Name
	if title == "" {
		title = filepath.Base(status.Path)
	}
	commands := extractLarkCLICommandHints(body, 14)
	references := listLarkSkillReferences(status.Path, 12)
	lines := []string{
		"## 官方 Skill: " + title,
		"Path: " + status.Path,
		"",
		"关键命令线索：",
	}
	if len(commands) == 0 {
		lines = append(lines, "- 未在 SKILL.md 中提取到 lark-cli 示例；请阅读正文规则。")
	} else {
		for _, cmd := range commands {
			lines = append(lines, "- "+cmd)
		}
	}
	if len(references) > 0 {
		lines = append(lines, "", "可用 references：")
		for _, ref := range references {
			lines = append(lines, "- "+ref)
		}
	}
	lines = append(lines, "", "SKILL.md 正文：", body)
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if limit > 0 && len(out) > limit {
		out = out[:limit] + "\n...[truncated; 如需完整内容请再次调用 lark_skill_view]"
	}
	return out
}

func stripYAMLFrontmatter(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return text
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return text
}

func extractLarkCLICommandHints(text string, limit int) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, limit)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(strings.Trim(line, "`"))
		idx := strings.Index(line, "lark-cli ")
		if idx < 0 {
			continue
		}
		line = strings.TrimSpace(line[idx:])
		if len(line) > 220 {
			line = line[:220] + "..."
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func listLarkSkillReferences(skillPath string, limit int) []string {
	refDir := filepath.Join(skillPath, "references")
	entries, err := os.ReadDir(refDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			names = append(names, "references/"+name)
		}
	}
	sort.Strings(names)
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	return names
}
