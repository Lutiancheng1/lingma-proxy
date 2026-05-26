package feishu

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxSkillFileBytes = 512 * 1024

type BridgeSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	WhenToUse   string   `json:"whenToUse,omitempty"`
	Path        string   `json:"path"`
	Source      string   `json:"source,omitempty"`
	Hash        string   `json:"hash"`
	Enabled     bool     `json:"enabled"`
	Error       string   `json:"error,omitempty"`
	Scripts     []string `json:"scripts,omitempty"`
	CreatedAt   string   `json:"createdAt,omitempty"`
	UpdatedAt   string   `json:"updatedAt,omitempty"`
}

type BridgeSkillImportResult struct {
	Imported []BridgeSkill `json:"imported"`
	Errors   []string      `json:"errors,omitempty"`
}

type BridgeSkillService struct {
	mu       sync.RWMutex
	rootDir  string
	store    *bridgeStore
	skills   map[string]BridgeSkill
	lastScan time.Time
}

func NewBridgeSkillService(dataDir string, store *bridgeStore) (*BridgeSkillService, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return &BridgeSkillService{skills: map[string]BridgeSkill{}}, nil
	}
	root := filepath.Join(dataDir, "feishu-skills")
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	svc := &BridgeSkillService{rootDir: root, store: store, skills: map[string]BridgeSkill{}}
	if err := svc.Reload(context.Background()); err != nil {
		return svc, err
	}
	return svc, nil
}

func (s *BridgeSkillService) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := map[string]BridgeSkill{}
	if s.store != nil {
		skills, err := s.store.LoadSkills(ctx)
		if err == nil {
			for _, skill := range skills {
				skill.Scripts = listSkillScripts(skill.Path)
				loaded[skill.ID] = skill
			}
		}
	}
	if s.rootDir != "" {
		entries, _ := os.ReadDir(s.rootDir)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(s.rootDir, entry.Name())
			skill, err := parseBridgeSkillDir(path, "local")
			if err != nil {
				continue
			}
			if existing, ok := loaded[skill.ID]; ok {
				skill.Enabled = existing.Enabled
				skill.CreatedAt = existing.CreatedAt
			}
			if skill.CreatedAt == "" {
				skill.CreatedAt = time.Now().Format(time.RFC3339)
			}
			skill.UpdatedAt = time.Now().Format(time.RFC3339)
			loaded[skill.ID] = skill
			if s.store != nil {
				_ = s.store.UpsertSkill(ctx, skill)
			}
		}
	}
	s.skills = loaded
	s.lastScan = time.Now()
	return nil
}

func (s *BridgeSkillService) List(enabledOnly bool) []BridgeSkill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BridgeSkill, 0, len(s.skills))
	for _, skill := range s.skills {
		if enabledOnly && !skill.Enabled {
			continue
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (s *BridgeSkillService) Find(nameOrID string) (BridgeSkill, bool) {
	target := strings.ToLower(strings.TrimSpace(nameOrID))
	if target == "" {
		return BridgeSkill{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if skill, ok := s.skills[target]; ok {
		return skill, true
	}
	for _, skill := range s.skills {
		if strings.EqualFold(skill.ID, nameOrID) || strings.EqualFold(skill.Name, nameOrID) {
			return skill, true
		}
	}
	return BridgeSkill{}, false
}

func (s *BridgeSkillService) ImportPath(ctx context.Context, sourcePath string) (BridgeSkillImportResult, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return BridgeSkillImportResult{}, fmt.Errorf("path is required")
	}
	if s.rootDir == "" {
		return BridgeSkillImportResult{}, fmt.Errorf("skill root is not configured")
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return BridgeSkillImportResult{}, err
	}
	tmpDir, err := os.MkdirTemp("", "feishu-skill-import-*")
	if err != nil {
		return BridgeSkillImportResult{}, err
	}
	defer os.RemoveAll(tmpDir)

	var candidates []string
	if info.IsDir() {
		dst := filepath.Join(tmpDir, filepath.Base(sourcePath))
		if err := copySkillDir(sourcePath, dst); err != nil {
			return BridgeSkillImportResult{}, err
		}
		candidates = append(candidates, dst)
	} else if strings.EqualFold(filepath.Ext(sourcePath), ".zip") {
		if err := unzipSkillArchive(sourcePath, tmpDir); err != nil {
			return BridgeSkillImportResult{}, err
		}
		candidates = findSkillDirs(tmpDir)
	} else {
		return BridgeSkillImportResult{}, fmt.Errorf("只支持 SKILL.md 文件夹或 zip 压缩包")
	}
	if len(candidates) == 0 {
		return BridgeSkillImportResult{}, fmt.Errorf("未找到包含 SKILL.md 的 skill 目录")
	}

	result := BridgeSkillImportResult{}
	for _, candidate := range candidates {
		skill, err := parseBridgeSkillDir(candidate, sourcePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		target := filepath.Join(s.rootDir, skill.ID)
		_ = os.RemoveAll(target)
		if err := copySkillDir(candidate, target); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", skill.Name, err))
			continue
		}
		skill.Path = target
		skill.Scripts = listSkillScripts(target)
		if skill.CreatedAt == "" {
			skill.CreatedAt = time.Now().Format(time.RFC3339)
		}
		skill.UpdatedAt = time.Now().Format(time.RFC3339)
		if s.store != nil {
			_ = s.store.UpsertSkill(ctx, skill)
		}
		s.mu.Lock()
		s.skills[skill.ID] = skill
		s.mu.Unlock()
		result.Imported = append(result.Imported, skill)
	}
	return result, nil
}

func (s *BridgeSkillService) SetEnabled(ctx context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	skill, ok := s.skills[id]
	if !ok {
		return fmt.Errorf("skill not found: %s", id)
	}
	skill.Enabled = enabled
	skill.UpdatedAt = time.Now().Format(time.RFC3339)
	s.skills[id] = skill
	if s.store != nil {
		return s.store.SetSkillEnabled(ctx, id, enabled)
	}
	return nil
}

func (s *BridgeSkillService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	skill, ok := s.skills[id]
	if ok {
		delete(s.skills, id)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("skill not found: %s", id)
	}
	if s.rootDir != "" && isPathInside(s.rootDir, skill.Path) {
		_ = os.RemoveAll(skill.Path)
	}
	if s.store != nil {
		return s.store.DeleteSkill(ctx, id)
	}
	return nil
}

func (s *BridgeSkillService) SkillBody(nameOrID string) (string, BridgeSkill, error) {
	skill, ok := s.Find(nameOrID)
	if !ok {
		return "", BridgeSkill{}, fmt.Errorf("skill not found: %s", nameOrID)
	}
	if !skill.Enabled {
		return "", skill, fmt.Errorf("skill is disabled: %s", skill.Name)
	}
	path := filepath.Join(skill.Path, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", skill, err
	}
	return string(data), skill, nil
}

func (s *BridgeSkillService) PromptListing(limit int) string {
	skills := s.List(true)
	if len(skills) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(skills) {
		limit = len(skills)
	}
	lines := []string{"可用用户导入 Skills（只列索引；需要正文时调用 skill_view）："}
	for _, skill := range skills[:limit] {
		desc := strings.TrimSpace(skill.Description)
		if skill.WhenToUse != "" {
			desc = strings.TrimSpace(desc + "；使用场景：" + skill.WhenToUse)
		}
		lines = append(lines, fmt.Sprintf("- %s：%s", skill.Name, summarizeText(desc, 220)))
	}
	if len(skills) > limit {
		lines = append(lines, fmt.Sprintf("- 另有 %d 个 skill，可用 skill_search 查询。", len(skills)-limit))
	}
	return strings.Join(lines, "\n")
}

func parseBridgeSkillDir(dir string, source string) (BridgeSkill, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return BridgeSkill{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return BridgeSkill{}, fmt.Errorf("skill 根目录不能是软链")
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return BridgeSkill{}, fmt.Errorf("缺少 SKILL.md")
	}
	if len(data) > maxSkillFileBytes {
		return BridgeSkill{}, fmt.Errorf("SKILL.md 超过 %d bytes", maxSkillFileBytes)
	}
	frontmatter, body := parseSkillFrontmatter(string(data))
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = filepath.Base(dir)
	}
	desc := strings.TrimSpace(frontmatter["description"])
	if desc == "" {
		desc = firstMarkdownParagraph(body)
	}
	hash := hashSkillDir(dir)
	id := normalizedSkillID(name, hash)
	return BridgeSkill{
		ID:          id,
		Name:        name,
		Description: desc,
		Version:     strings.TrimSpace(frontmatter["version"]),
		WhenToUse:   strings.TrimSpace(frontmatter["when_to_use"]),
		Path:        dir,
		Source:      source,
		Hash:        hash,
		Enabled:     true,
		Scripts:     listSkillScripts(dir),
	}, nil
}

func parseSkillFrontmatter(text string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(text, "---") {
		return out, text
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) != 3 {
		return out, text
	}
	for _, raw := range strings.Split(parts[1], "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		out[strings.TrimSpace(key)] = value
	}
	return out, parts[2]
}

func firstMarkdownParagraph(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "#"))
		if line != "" {
			return summarizeText(line, 220)
		}
	}
	return ""
}

func normalizedSkillID(name, hash string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-", ":", "-")
	base = replacer.Replace(base)
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "skill"
	}
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return base + "-" + hash
}

func hashSkillDir(dir string) string {
	h := sha256.New()
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(filepath.ToSlash(rel)))
		data, err := os.ReadFile(path)
		if err == nil {
			h.Write(data)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

func listSkillScripts(dir string) []string {
	scriptsDir := filepath.Join(dir, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

func findSkillDirs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil {
			out = append(out, path)
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

func copySkillDir(src, dst string) error {
	srcReal, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(srcReal, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不允许导入软链: %s", path)
		}
		rel, err := filepath.Rel(srcReal, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if !isPathInside(dst, target) {
			return fmt.Errorf("非法路径: %s", path)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持的文件类型: %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

func unzipSkillArchive(zipPath, dst string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		name := filepath.Clean(file.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("zip 内含非法路径: %s", file.Name)
		}
		target := filepath.Join(dst, name)
		if !isPathInside(dst, target) {
			return fmt.Errorf("zip 路径逃逸: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip 内不允许软链: %s", file.Name)
		}
		if file.UncompressedSize64 > 10*1024*1024 {
			return fmt.Errorf("zip 内文件超过 10MB 限制: %s", file.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode().Perm())
		if err != nil {
			_ = src.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(src, 10*1024*1024))
		_ = src.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func isPathInside(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
