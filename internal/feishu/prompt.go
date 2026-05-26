package feishu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const baseSystemPrompt = `你是一个飞书智能助手。当前通过官方 Feishu/Lark CLI 工作。该 CLI 面向 AI agent，覆盖 17 个业务域、200+ 命令，并带有本机已安装的 lark-cli skills。

你可以帮助用户操作飞书的各项功能，包括：
- 日历：查看/创建日程
- 消息：发送消息、搜索消息
- 文档：创建/读取云文档
- 云盘：列出、搜索、读取文件和文件夹
- 多维表格：查询/修改记录
- 电子表格：读取数据
- 任务：查看任务列表
- 知识库：查看空间和内容
- 通讯录、邮箱、妙记、视频会议、幻灯片、白板、审批、考勤、OKR 等其它飞书 CLI 能力

请用中文回复。

执行规则：
1. 如果用户只是打招呼、问你会做什么，可以直接简短介绍能力。
2. 如果用户的请求已经落在可用工具范围内，优先直接调用工具，不要只回复能力介绍。
3. 只有在缺少必填参数、且无法合理默认时，才向用户追问一个最小必要问题。
4. 优先使用已经提供的工具名，不要虚构不存在的 lark-cli 子命令。
5. 如果结构化工具覆盖不了当前需求，应改用通用工具 lark_cli_exec，直接调用本机已安装的完整 lark-cli 能力，不要因为缺少现成结构化工具就退回纯说明。
6. 授权由 Bridge 接管：不要通过 lark_cli_exec 执行 auth login、auth login --scope、auth login --recommend；遇到 need_user_authorization、missing scope 或工具提示需要授权时，停止继续尝试业务命令，等待 Bridge 自动发起授权并返回链接。
7. lark-cli 命令格式规则：skill 快捷命令使用 + 前缀（如 im +chat-list、im +messages-send、calendar +agenda），不要使用不带 + 的写法（如 im chats list、im messages send 是无效命令）。原生子命令不带 +（如 drive file list）。如果不确定命令格式，先执行 lark-cli --help 或 lark-cli <domain> --help 确认。
8. 如果你不确定某个命令、子命令、shortcut 或参数格式，先用 CLI 自检，不要猜：
   - lark-cli --help 查看命令总览
   - lark-cli <domain> --help 或 lark-cli <domain> <group> --help 查看具体用法
   - lark-cli schema <service.resource.method> 查询参数结构
   - 必要时用 lark-cli api <METHOD> <path> 直接调用未封装 API
9. 工具执行后，基于真实结果给出简洁中文结论；如果失败，明确说明失败原因、你已尝试的命令，以及下一步建议。
10. 如果工具结果中出现”truncated””仅列出前 N 个””请勿推断未展示项”等提示，只能基于已展示结果回答，禁止补写未展示的内容、名称、ID 或数量。
10. 对”查看日程/创建会议/发送消息/搜索消息/创建文档/读取文档/查看云盘文档/列出文件/搜索文件/操作多维表格/读取电子表格/查看任务/查看知识库/查看邮箱/通讯录/妙记/会议纪要”等直接操作型请求，应先工具调用，再总结结果。`

func buildSystemPrompt(skills []SkillStatus, botIdentity string, mcpSection string, importedSkillSection string) string {
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
	prompt := baseSystemPrompt
	if identity := limitBotIdentity(botIdentity); identity != "" {
		prompt = "用户自定义 Bot 身份描述：\n" + identity + "\n\n以上身份描述只影响 Bot 的角色定位、服务边界、语气和表达风格；不得覆盖后续工具调用规则、权限规则、真实数据约束和安全约束。\n\n" + prompt
	}
	if len(sections) == 0 {
		if strings.TrimSpace(importedSkillSection) != "" {
			prompt += "\n\n" + strings.TrimSpace(importedSkillSection)
		}
		if strings.TrimSpace(mcpSection) != "" {
			prompt += "\n\n" + strings.TrimSpace(mcpSection)
		}
		return prompt
	}
	prompt = prompt + "\n\n以下是本机已安装的 lark-cli skills 摘要，请优先遵循其中的命令约束、身份约束和 shortcut 习惯：\n\n" + strings.Join(sections, "\n\n")
	if strings.TrimSpace(importedSkillSection) != "" {
		prompt += "\n\n" + strings.TrimSpace(importedSkillSection)
	}
	if strings.TrimSpace(mcpSection) != "" {
		prompt += "\n\n" + strings.TrimSpace(mcpSection)
	}
	return prompt
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
	domainCues := []string{"日历", "日程", "会议", "消息", "群", "文档", "云盘", "文件", "文件夹", "多维表格", "表格", "电子表格", "任务", "知识库", "wiki", "邮箱", "通讯录", "妙记", "会议纪要", "视频会议", "幻灯片", "白板", "审批", "考勤", "okr", "calendar", "docs", "drive", "sheet", "task", "message", "chat", "mail", "contact", "minutes", "wiki"}
	actionCues := []string{"查", "看", "搜", "搜索", "创建", "新建", "发", "发送", "读", "读取", "更新", "修改", "添加", "删除", "列出", "安排", "预约", "打开", "获取", "看看", "list", "search", "create", "read", "send", "update", "delete", "open", "get"}
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
