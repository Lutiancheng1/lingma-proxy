package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var requiredSkillNames = []string{
	"lark-shared",
	"lark-calendar",
	"lark-im",
	"lark-doc",
	"lark-base",
	"lark-sheets",
	"lark-task",
	"lark-wiki",
}

type listedSkill struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Scope  string   `json:"scope"`
	Agents []string `json:"agents"`
}

func discoverSkills(ctx context.Context) ([]SkillStatus, error) {
	cmd := exec.CommandContext(ctx, "npx", "skills", "ls", "-g", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list skills failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var listed []listedSkill
	if err := json.Unmarshal(output, &listed); err != nil {
		return nil, fmt.Errorf("parse skills list failed: %w", err)
	}
	index := make(map[string]listedSkill, len(listed))
	for _, item := range listed {
		index[item.Name] = item
	}
	statuses := make([]SkillStatus, 0, len(requiredSkillNames))
	for _, name := range requiredSkillNames {
		status := SkillStatus{Name: name}
		item, ok := index[name]
		if !ok {
			status.Message = "未通过 skills 工具发现"
			statuses = append(statuses, status)
			continue
		}
		realPath, err := filepath.EvalSymlinks(item.Path)
		if err != nil {
			realPath = item.Path
		}
		skillFile := filepath.Join(realPath, "SKILL.md")
		info, statErr := os.Stat(skillFile)
		if statErr != nil || info.IsDir() {
			status.Path = realPath
			status.Message = "SKILL.md 不存在"
			statuses = append(statuses, status)
			continue
		}
		status.Found = true
		status.Path = realPath
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
