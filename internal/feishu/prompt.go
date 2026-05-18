package feishu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const baseSystemPrompt = `你是一个飞书智能助手。你可以帮助用户操作飞书的各项功能，包括：
- 日历：查看/创建日程
- 消息：发送消息、搜索消息
- 文档：创建/读取云文档
- 多维表格：查询/修改记录
- 电子表格：读取数据
- 任务：查看任务列表
- 知识库：查看空间和内容

请用中文回复。

执行规则：
1. 如果用户只是打招呼、问你会做什么，可以直接简短介绍能力。
2. 如果用户的请求已经落在可用工具范围内，优先直接调用工具，不要只回复能力介绍。
3. 只有在缺少必填参数、且无法合理默认时，才向用户追问一个最小必要问题。
4. 优先使用已经提供的工具名，不要虚构不存在的 lark-cli 子命令。
5. 工具执行后，基于真实结果给出简洁中文结论；如果失败，明确说明失败原因和下一步。
6. 对“查看日程/创建会议/发送消息/搜索消息/创建文档/读取文档/操作多维表格/读取电子表格/查看任务/查看知识库”等直接操作型请求，应先工具调用，再总结结果。`

func buildSystemPrompt(skills []SkillStatus) string {
	sections := make([]string, 0, len(skills))
	for _, skill := range skills {
		if !skill.Found || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		skillPath := filepath.Join(skill.Path, "SKILL.md")
		excerpt, err := loadSkillExcerpt(skillPath)
		if err != nil || excerpt == "" {
			continue
		}
		sections = append(sections, fmt.Sprintf("## %s\n%s", skill.Name, excerpt))
	}
	if len(sections) == 0 {
		return baseSystemPrompt
	}
	return baseSystemPrompt + "\n\n以下是本机已安装的 lark-cli skills 摘要，请优先遵循其中的命令约束、身份约束和 shortcut 习惯：\n\n" + strings.Join(sections, "\n\n")
}

func loadSkillExcerpt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	if strings.HasPrefix(text, "---") {
		parts := strings.SplitN(text, "---", 3)
		if len(parts) == 3 {
			text = parts[2]
		}
	}
	lines := make([]string, 0, 60)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		lines = append(lines, line)
		if len(lines) >= 60 {
			break
		}
	}
	excerpt := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(excerpt) > 2200 {
		excerpt = excerpt[:2200] + "\n...[truncated]"
	}
	return excerpt, nil
}

func shouldForceToolUse(userText string) bool {
	text := strings.TrimSpace(userText)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	introCues := []string{"你会什么", "能做什么", "你是谁", "介绍一下", "help", "功能", "你可以"}
	for _, cue := range introCues {
		if strings.Contains(text, cue) || strings.Contains(lower, cue) {
			return false
		}
	}
	domainCues := []string{"日历", "日程", "会议", "消息", "群", "文档", "多维表格", "表格", "电子表格", "任务", "知识库", "wiki", "calendar", "docs", "sheet", "task", "message", "chat"}
	actionCues := []string{"查", "看", "搜", "搜索", "创建", "新建", "发", "发送", "读", "读取", "更新", "修改", "添加", "删除", "列出", "安排", "预约", "list", "search", "create", "read", "send", "update", "delete"}
	hasDomain := false
	for _, cue := range domainCues {
		if strings.Contains(text, cue) || strings.Contains(lower, cue) {
			hasDomain = true
			break
		}
	}
	hasAction := false
	for _, cue := range actionCues {
		if strings.Contains(text, cue) || strings.Contains(lower, cue) {
			hasAction = true
			break
		}
	}
	return hasDomain && hasAction
}
