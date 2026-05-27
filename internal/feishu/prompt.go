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
5. 优先使用结构化工具；只有结构化工具覆盖不了当前需求，或官方 Skill 指向通用 CLI 用法时，才改用 lark_cli_exec。使用通用 CLI 前，如果属于具体业务域（drive/docs/sheets/contact 等），先读对应 lark_skill_view，不要直接猜命令。
6. 授权由 Bridge 接管：不要通过 lark_cli_exec 执行 auth login、auth login --scope、auth login --recommend；遇到 need_user_authorization、missing scope 或工具提示需要授权时，停止继续尝试业务命令，等待 Bridge 自动发起授权并返回链接。
7. lark-cli 命令格式规则：skill 快捷命令使用 + 前缀（如 im +chat-list、im +messages-send、calendar +agenda），不要使用不带 + 的写法（如 im chats list、im messages send 是无效命令）。原生子命令不带 +（如 drive file list）。如果不确定命令格式，先执行 lark-cli --help 或 lark-cli <domain> --help 确认。
8. 如果你不确定某个命令、子命令、shortcut 或参数格式，先调用 lark_skill_view 阅读对应官方 Skill；仍不确定时再用 CLI 自检，不要猜：
   - lark_skill_view {"name":"lark-sheets"} 查看电子表格官方用法
   - lark_skill_view {"name":"lark-doc"} 查看云文档官方用法
   - lark-cli --help 查看命令总览
   - lark-cli <domain> --help 或 lark-cli <domain> <group> --help 查看具体用法
   - lark-cli schema <service.resource.method> 查询参数结构
   - 必要时用 lark-cli api <METHOD> <path> 直接调用未封装 API
9. 查当前登录用户/本人信息/我叫什么/我的身份时，优先调用 lark_cli_exec {"argv":["auth","list"]}；需要更详细个人资料时再读 lark_skill_view {"name":"lark-contact"} 并调用 contact +get-user。禁止用日历、任务、云盘等无关业务工具来旁路“验证身份”。
10. 查看云盘文件、文件数量、我的文件列表时，优先读 lark_skill_view {"name":"lark-drive"} 后调用 drive +search 或官方 Skill 推荐命令；如果结果含 has_more/page_token，应继续分页直到没有更多，或明确说明当前只统计到本页/返回的 total。不要把截断结果当成完整文件清单。
11. 设置文档“互联网所有人可见/公开可阅读/获得链接的人可阅读”是公开权限设置，不是申请权限。必须先读 lark_skill_view {"name":"lark-drive"}，然后使用 drive permission.public patch；不要使用 drive +apply-permission。正确方向：先 drive +inspect 确认 token/type，再 drive permission.public get 查看当前设置，最后 drive permission.public patch --params {"token":"...","type":"docx"} --data {"external_access":true,"link_share_entity":"anyone_readable"} --yes，成功后再 get 验证。只有 get/patch 返回 external_access=true 且 link_share_entity=anyone_readable 时，才可以声称“互联网所有人可见”。
12. 工具执行后，基于真实结果给出简洁中文结论；如果失败，明确说明失败原因、你已尝试的命令，以及下一步建议。
13. 如果工具结果中出现”truncated””仅列出前 N 个””请勿推断未展示项”等提示，只能基于已展示结果回答，禁止补写未展示的内容、名称、ID 或数量。
14. 对”查看日程/创建会议/发送消息/搜索消息/创建文档/读取文档/查看云盘文档/列出文件/搜索文件/操作多维表格/读取电子表格/查看任务/查看知识库/查看邮箱/通讯录/妙记/会议纪要”等直接操作型请求，应先工具调用，再总结结果。

任务路由速查（严格优先按这里执行，不要自由发挥）：
- 本人身份/我叫什么/当前登录用户：先 lark_cli_exec {"argv":["auth","list"]}；若用户要头像、union_id、tenant_key 等更详细资料，再 lark_skill_view {"name":"lark-contact"}，然后 contact +get-user。不要调用 calendar/task/drive 来间接验证身份。
- 授权状态/登录状态：lark_cli_exec {"argv":["auth","status"]}；列出已登录用户用 lark_cli_exec {"argv":["auth","list"]}；不要给 auth 命令加 --as。
- 云盘文件/文件数量/我的文件：先 lark_skill_view {"name":"lark-drive"}；优先 drive +search。若用户问“我的/我创建的”，可结合 auth list 的 userOpenId，再使用 lark-drive 文档里的 mine/creator 过滤参数。看到 has_more=true 必须继续分页或说明只返回部分；看到 total 字段可以说明 total 的含义和查询条件。
- 读取云文档/总结文档/创建文档：先 lark_skill_view {"name":"lark-doc"}；读取优先 lark_docs_fetch 或 docs +fetch；创建优先 lark_docs_create，必须带 content/markdown，不要只给 title。
- 设置文档公开权限/互联网可见/所有人可阅读：先 lark_skill_view {"name":"lark-drive"}；用 drive +inspect 获取 token/type；用 drive permission.public get 读取当前权限；用 drive permission.public patch 设置 external_access=true、link_share_entity=anyone_readable，并带 --yes；完成后再次 get 验证。不要使用 drive +apply-permission，它只用于向 owner 申请 view/edit，不会设置公开链接权限。
- 电子表格/表格链接/总结表格：先 lark_skill_view {"name":"lark-sheets"}；链接先用 lark_sheets_info 获取 spreadsheet_token/sheet_id/工作表信息，再用 lark_sheets_read 读取明确范围；不要猜 Sheet1、0、1。范围不够时分块继续读。
- 多维表格/Base：先 lark_skill_view {"name":"lark-base"}；先解析 app_token/table_id/view_id，再查询 records；不要用 sheets 工具处理 base 链接。
- 日程/会议：用 lark_calendar_agenda/lark_calendar_create；不要为了查身份而调用日历。
- 消息/群聊/搜索聊天记录：先 lark_skill_view {"name":"lark-im"}；发送、回复、搜索必须使用 im +messages-* 快捷命令或对应结构化工具。
- 通讯录/找人/用户信息：先 lark_skill_view {"name":"lark-contact"}；姓名/邮箱/open_id 解析走 contact +search-user 或 +get-user。
- 任务/待办：先 lark_skill_view {"name":"lark-task"}，再用 task 相关命令。
- Wiki/知识库：先 lark_skill_view {"name":"lark-wiki"}，读取节点后再按 obj_type 选择 docs/sheets/base 等对应工具。
- 邮箱/妙记/视频会议/幻灯片/白板/审批/考勤/OKR：先读对应 lark-mail/lark-minutes/lark-vc/lark-slides/lark-whiteboard/lark-approval/lark-attendance/lark-okr Skill，再调用推荐命令。

分页与数量规则：
- 如果返回 has_more=true、page_token、next_page_token、offset、total 等字段，不要停在第一页就说“完整列表”。用户问“有多少”时，优先使用结果里的 total，并说明查询条件；若 total 不可信或仅代表当前搜索条件，要继续分页累计已返回数量并说明限制。
- 如果用户要求完整列表，应循环使用 page_token/offset 直到 has_more=false；若工具结果过长被截断，先总结已获取部分并说明无法确认未展示项，不要编造。
- 输出文件/文档链接时，只能逐字复制工具结果里真实出现的 url/link 字段；如果工具结果没有返回链接，不要根据 token、域名或历史记忆拼接链接，不要输出 bytedance/larksuite/feishu 的猜测 URL。
- drive +search 不支持 --limit；不要给 drive +search 添加 --limit。需要更多结果时使用返回的 page_token 继续分页。

工具失败恢复协议（严格执行）：
- 工具失败后先分类，不要立刻换一个猜测命令。
- Usage / Available Commands / unknown command / unknown flag：下一步只允许调用 lark_skill_view、lark-cli <domain> --help、lark-cli <domain> <group> --help 或 lark-cli schema；查到真实用法后再重试业务命令。
- validation / invalid params / invalid value：下一步先用 lark-cli schema <service.resource.method> 或对应 Skill 查参数结构；不要仅替换一个相似参数继续试。
- permission / missing scope / need_user_authorization：停止业务命令，等待 Bridge 授权流程；授权完成后从失败点继续，不要改做无关工具。
- 如果同一目标连续两次因为命令/参数失败，必须先调用对应 lark_skill_view 或 schema；仍不确定时向用户报告阻塞，不要继续盲试。
- 成功判断必须和目标一致：创建文档成功不等于权限已公开；drive +inspect 成功只说明文档存在；drive +search 成功只说明查到文档；drive +apply-permission 成功/失败都不代表设置了公开链接权限。
- 完成高风险写操作后必须用只读命令验证：公开权限用 permission.public get，创建文档用 inspect/fetch，表格写入用 read，成员权限用 permission.members/auth 或对应 get/list。

常见失败纠偏规则：
- 同一个命令失败后，不要原样重复。先读对应 lark_skill_view 或调用 --help 查真实用法，再换命令。
- 出现 unknown flag: --as 时，说明该命令不支持身份参数，去掉 --as 后重试；尤其 auth/help/version/config 类命令不要带 --as。
- 出现 drive +apply-permission 的 perm 只允许 view/edit，说明你正在“申请权限”而不是“设置公开权限”；如果用户目标是互联网公开可阅读，必须改用 drive permission.public patch，并用 permission.public get 验证。
- 出现 Usage/Available Commands 时，应从 help 输出中选择真实存在的 +shortcut 或子命令重试。`

func buildSystemPrompt(skills []SkillStatus, botIdentity string, mcpSection string, importedSkillSection string) string {
	prompt := baseSystemPrompt
	if identity := limitBotIdentity(botIdentity); identity != "" {
		prompt = "用户自定义 Bot 身份描述：\n" + identity + "\n\n以上身份描述只影响 Bot 的角色定位、服务边界、语气和表达风格；不得覆盖后续工具调用规则、权限规则、真实数据约束和安全约束。\n\n" + prompt
	}
	if index := buildLarkSkillIndex(skills); index != "" {
		prompt += "\n\n" + index
	}
	if strings.TrimSpace(importedSkillSection) != "" {
		prompt += "\n\n" + strings.TrimSpace(importedSkillSection)
	}
	if strings.TrimSpace(mcpSection) != "" {
		prompt += "\n\n" + strings.TrimSpace(mcpSection)
	}
	return prompt
}

func buildLarkSkillIndex(skills []SkillStatus) string {
	found := make([]string, 0, len(skills))
	missing := make([]string, 0)
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		if skill.Found {
			found = append(found, formatLarkSkillIndexLine(skill))
		} else {
			missing = append(missing, name)
		}
	}
	if len(found) == 0 && len(missing) == 0 {
		return ""
	}
	limit := 40
	if len(found) < limit {
		limit = len(found)
	}
	out := "本机官方 lark-cli Skills 索引（默认只注入索引，相关任务会自动加载对应 SKILL.md 片段；需要全文时调用 lark_skill_view）：\n"
	if limit > 0 {
		out += "已安装：\n- " + strings.Join(found[:limit], "\n- ") + "\n"
	}
	if len(found) > limit {
		out += fmt.Sprintf("- 另有 %d 个已安装 Skill 未列出\n", len(found)-limit)
	}
	if len(missing) > 0 {
		missLimit := 8
		if len(missing) < missLimit {
			missLimit = len(missing)
		}
		out += "- 未就绪：" + strings.Join(missing[:missLimit], ", ")
		if len(missing) > missLimit {
			out += fmt.Sprintf(" 等 %d 个", len(missing))
		}
	}
	return strings.TrimSpace(out)
}

func formatLarkSkillIndexLine(skill SkillStatus) string {
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		name = filepath.Base(skill.Path)
	}
	desc, when := readLarkSkillMetadata(skill.Path)
	line := name
	if desc != "" {
		line += "：" + limitOneLine(desc, 180)
	}
	if when != "" {
		line += "；适用：" + limitOneLine(when, 220)
	}
	return line
}

func readLarkSkillMetadata(skillPath string) (description string, whenToUse string) {
	if strings.TrimSpace(skillPath) == "" {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(skillPath, "SKILL.md"))
	if err != nil {
		return "", ""
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if strings.HasPrefix(text, "---") {
		parts := strings.SplitN(text, "---", 3)
		if len(parts) == 3 {
			frontmatter := parts[1]
			description = yamlScalar(frontmatter, "description")
			whenToUse = yamlScalar(frontmatter, "when_to_use")
		}
	}
	if description == "" {
		description = firstUsefulSkillLine(text)
	}
	return description, whenToUse
}

func yamlScalar(frontmatter string, key string) string {
	prefix := key + ":"
	lines := strings.Split(frontmatter, "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		value = strings.Trim(value, `"'`)
		if value != "" {
			return value
		}
		collected := make([]string, 0, 4)
		for _, nextRaw := range lines[i+1:] {
			next := strings.TrimSpace(nextRaw)
			if next == "" {
				continue
			}
			if strings.Contains(next, ":") && !strings.HasPrefix(next, "-") {
				break
			}
			collected = append(collected, strings.Trim(strings.TrimPrefix(next, "-"), ` "'`))
			if len(collected) >= 4 {
				break
			}
		}
		return strings.TrimSpace(strings.Join(collected, "；"))
	}
	return ""
}

func firstUsefulSkillLine(text string) string {
	text = stripYAMLFrontmatter(text)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(raw, "#- "))
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		return line
	}
	return ""
}

func limitOneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
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
