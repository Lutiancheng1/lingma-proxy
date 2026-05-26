package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolExecutionResult struct {
	Output        string
	MissingScopes []string
	NeedsLogin    bool
	ConsoleURL    string
	Hint          string
	IsError       bool
}

type permissionRequirement struct {
	Scopes     []string
	NeedsLogin bool
	ConsoleURL string
	Hint       string
}

var scopeHintPattern = regexp.MustCompile(`auth login --scope\s+"([^"]+)"`)

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_cli_exec",
				"description": "通用飞书 CLI 执行入口。适用于当前未单独结构化建模的 lark-cli 能力。传入 argv 数组，不要包含程序名 lark-cli；已知飞书业务域命令如果未显式指定 --as，则会自动追加 --as user；auth、--help/-h 和未知根命令不会追加 --as。\n\n强制规则：\n- 如果准备使用某个业务域但不确定命令/参数，先调用 lark_skill_view 阅读对应官方 Skill（如 lark-sheets、lark-doc、lark-drive），再执行本工具；不要凭经验猜 Sheet1/0/1、docs/file/list 等命令\n- 授权不要通过本工具执行 auth login；遇到 need_user_authorization 时 Bridge 会自动发起登录并返回授权链接\n- 查询当前身份优先用 argv：[\"auth\", \"status\"]；查看已登录用户用 argv：[\"auth\", \"list\"]\n- lark-cli 的子命令分两类：原生子命令（如 drive file list）和 skill 快捷命令（带 + 前缀，如 im +chat-list、im +messages-send）\n- IM 相关操作必须使用 + 前缀快捷命令：im +chat-list、im +chat-create、im +messages-send、im +messages-search、im +messages-reply\n- 示例 argv：[\"im\", \"+chat-list\", \"--limit\", \"10\"]、[\"drive\", \"file\", \"list\"]、[\"calendar\", \"+agenda\"]",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"argv": map[string]any{
							"type":        "array",
							"description": "lark-cli 的参数数组，例如 [\"drive\", \"file\", \"list\"] 或 [\"mail\", \"thread\", \"list\", \"--limit\", \"10\"]",
							"items":       map[string]any{"type": "string"},
						},
					},
					"required": []string{"argv"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_skill_view",
				"description": "读取本机已安装的官方飞书 lark-cli Skill 文档。用于在执行 lark_cli_exec 前确认真实命令、shortcut、参数和注意事项。支持传入 skill 名称或业务域，例如 lark-sheets/sheets/表格、lark-doc/docs/文档、lark-drive/drive/云盘。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "官方 Skill 名称或业务域，例如 lark-sheets、sheets、docs、drive"},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_calendar_agenda",
				"description": "查看今日日程安排。返回当天所有日历事件的标题、时间、参会人等信息。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"start": map[string]any{"type": "string", "description": "起始日期，格式 YYYY-MM-DD，默认今天"},
						"end":   map[string]any{"type": "string", "description": "结束日期，格式 YYYY-MM-DD，默认今天"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "mcp_call",
				"description": "调用已启用的本机 MCP 工具。仅在系统提示词列出可用 MCP server/tool 时使用；server 和 tool 必须精确匹配提示词中的名称。飞书数据优先用 lark-cli 工具，浏览器、文件、外部系统等通用扩展能力再用 MCP。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"server": map[string]any{"type": "string", "description": "MCP server 名称，例如 playwright-mcp-server"},
						"tool":   map[string]any{"type": "string", "description": "MCP tool 名称，例如 browser_navigate"},
						"arguments": map[string]any{
							"type":        "object",
							"description": "传给 MCP tool 的参数对象。必须符合该 tool 的 schema。",
						},
					},
					"required": []string{"server", "tool", "arguments"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_calendar_create",
				"description": "创建日历日程事件。需要提供主题、开始时间和结束时间。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary":     map[string]any{"type": "string", "description": "日程主题/标题"},
						"start_time":  map[string]any{"type": "string", "description": "开始时间，格式 YYYY-MM-DD HH:MM"},
						"end_time":    map[string]any{"type": "string", "description": "结束时间，格式 YYYY-MM-DD HH:MM"},
						"description": map[string]any{"type": "string", "description": "日程描述（可选）"},
					},
					"required": []string{"summary", "start_time", "end_time"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_im_send",
				"description": "发送飞书消息。可以发给个人（用 user_id）或群聊（用 chat_id）。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"receive_id":      map[string]any{"type": "string", "description": "接收者 ID（user_id 或 chat_id）"},
						"receive_id_type": map[string]any{"type": "string", "enum": []string{"chat_id", "user_id", "open_id", "email"}, "description": "接收者 ID 类型，默认 chat_id"},
						"text":            map[string]any{"type": "string", "description": "消息文本内容"},
						"msg_type":        map[string]any{"type": "string", "enum": []string{"text", "markdown"}, "description": "消息类型，默认 text"},
					},
					"required": []string{"receive_id", "text"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_im_search",
				"description": "搜索飞书消息。支持按关键词、发送人、时间范围过滤。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":   map[string]any{"type": "string", "description": "搜索关键词"},
						"chat_id": map[string]any{"type": "string", "description": "限定搜索的群聊 ID（可选）"},
						"from":    map[string]any{"type": "string", "description": "发送人筛选（可选）"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_docs_create",
				"description": "创建飞书云文档（docx 格式）。必须提供 markdown 正文；不要在尚未整理好内容时先创建空文档。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":        map[string]any{"type": "string", "description": "文档标题"},
						"markdown":     map[string]any{"type": "string", "description": "Lark-flavored Markdown 正文；创建文档时必填"},
						"folder_token": map[string]any{"type": "string", "description": "存放文件夹的 token（可选，默认根目录）"},
					},
					"required": []string{"title", "markdown"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_docs_fetch",
				"description": "读取飞书云文档内容。通过文档 URL 或 token 获取正文。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"doc_token": map[string]any{"type": "string", "description": "文档 token 或完整 URL"},
					},
					"required": []string{"doc_token"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_base_records",
				"description": "操作飞书多维表格（Bitable）记录。支持列出、创建、更新记录。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":    map[string]any{"type": "string", "enum": []string{"list", "create", "update"}, "description": "操作类型"},
						"app_token": map[string]any{"type": "string", "description": "多维表格 app_token"},
						"table_id":  map[string]any{"type": "string", "description": "数据表 table_id"},
						"filter":    map[string]any{"type": "string", "description": "过滤条件（list 时可选）"},
						"fields":    map[string]any{"type": "object", "description": "字段键值对（create/update 时必填）"},
						"record_id": map[string]any{"type": "string", "description": "记录 ID（update 时必填）"},
					},
					"required": []string{"action", "app_token", "table_id"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_sheets_info",
				"description": "读取飞书电子表格元数据和工作表 sheet_id。读取单元格前必须先用它确认真实 sheet_id，不要猜 Sheet1、0、1。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spreadsheet_token": map[string]any{"type": "string", "description": "表格 token"},
						"url":               map[string]any{"type": "string", "description": "表格完整 URL（有 URL 时可替代 token）"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_sheets_read",
				"description": "读取飞书电子表格单元格数据。优先先调用 lark_sheets_info 获取真实 sheet_id，再传 sheet_id + A1:D10 这类不带 sheet 前缀的 range；不要猜 Sheet1、0、1。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spreadsheet_token": map[string]any{"type": "string", "description": "表格 token"},
						"sheet_id":          map[string]any{"type": "string", "description": "真实 sheet_id，来自 lark_sheets_info"},
						"range":             map[string]any{"type": "string", "description": "读取范围，如 A1:D10；如果包含 ! 前缀，前缀必须是真实 sheet_id，不是 Sheet1/0/1 这类猜测值"},
					},
					"required": []string{"spreadsheet_token", "range"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_task_list",
				"description": "查看飞书任务列表。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"is_completed": map[string]any{"type": "boolean", "description": "是否只看已完成任务，默认 false"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_wiki_search",
				"description": "查看飞书知识库空间列表或搜索知识库内容。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":     map[string]any{"type": "string", "enum": []string{"list_spaces", "list_nodes", "get_node"}, "description": "操作：列出空间、列出节点、获取节点内容"},
						"space_id":   map[string]any{"type": "string", "description": "知识库空间 ID（list_nodes/get_node 时必填）"},
						"node_token": map[string]any{"type": "string", "description": "节点 token（get_node 时必填）"},
					},
					"required": []string{"action"},
				},
			},
		},
	}
}

func toolDefinitionsWithMCP(mcpTools []mcpTool) []map[string]any {
	defs := toolDefinitions()
	defs = append(defs, skillToolDefinitions()...)
	for _, tool := range mcpTools {
		name := strings.TrimSpace(tool.Function)
		if name == "" {
			continue
		}
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		description := strings.TrimSpace(tool.Description)
		if description == "" {
			description = "MCP tool"
		}
		description = fmt.Sprintf("[%s/%s] %s", tool.Server, tool.Name, description)
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  schema,
			},
		})
	}
	return defs
}

func skillToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "skill_list",
				"description": "列出用户在 Feishu Bridge 高级设置中导入并启用的 Skills。只返回索引，不返回完整正文。",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "skill_search",
				"description": "按关键词搜索用户导入的 Skills。用于选择最相关的 skill 后再调用 skill_view 读取正文。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "搜索关键词"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "skill_view",
				"description": "读取一个已启用 Skill 的完整 SKILL.md 正文。只有准备使用该 skill 时再调用，避免浪费上下文。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "Skill 名称或 ID"},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "skill_run_script",
				"description": "请求执行某个 Skill scripts/ 目录下实际存在的脚本。只有 skill_view 显示 scripts 存在时才调用；如果 Skill 文档要求 curl/HTTP API，请改用 skill_http_request，不要用 lark_cli_exec 运行 curl。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"skill":  map[string]any{"type": "string", "description": "Skill 名称或 ID"},
						"script": map[string]any{"type": "string", "description": "scripts/ 目录下的脚本文件名"},
						"args":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选参数"},
					},
					"required": []string{"skill", "script"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "skill_http_request",
				"description": "按已读取的 Skill 文档执行 HTTP API 请求。适用于无 scripts/、但 SKILL.md 要求访问公开 REST API 的 Skill。调用前必须先在本轮调用 skill_view 阅读对应 SKILL.md；URL 必须来自 skill_view 看到的 SKILL.md，不要自造域名、路径或把站点域名改成 API 子域；支持 GET/POST/PUT/PATCH/DELETE，默认超时和响应上限可在高级设置调整；不要用 lark_cli_exec 或 MCP 代替 curl/API 调用。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"skill":  map[string]any{"type": "string", "description": "关联的 Skill 名称或 ID，用于审计和确认该请求来自哪个 Skill"},
						"method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, "description": "HTTP method，默认 GET"},
						"url":    map[string]any{"type": "string", "description": "完整 HTTP/HTTPS URL"},
						"headers": map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{"type": "string"},
							"description":          "可选请求头，例如 User-Agent",
						},
						"body": map[string]any{"type": "string", "description": "可选请求体，通常为 JSON 字符串；GET/DELETE 一般不需要"},
					},
					"required": []string{"skill", "url"},
				},
			},
		},
	}
}

func isBridgeSkillTool(name string) bool {
	switch name {
	case "skill_list", "skill_search", "skill_view", "skill_run_script", "skill_http_get", "skill_http_request":
		return true
	default:
		return false
	}
}

func buildToolCommand(toolName string, args map[string]any) ([]string, error) {
	switch toolName {
	case "lark_cli_exec":
		argv := stringListArg(args, "argv")
		if len(argv) == 0 {
			return nil, fmt.Errorf("argv 不能为空")
		}
		if strings.EqualFold(argv[0], "lark-cli") {
			argv = argv[1:]
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("argv 不能为空")
		}
		argv = normalizeLarkCLIArgv(argv)
		if isLarkAuthArgv(argv) || isLarkHelpArgv(argv) {
			return append([]string{"lark-cli"}, argv...), nil
		}
		if shouldAutoAppendAs(argv) && !containsAsFlag(argv) {
			argv = append(argv, "--as", "user")
		}
		return append([]string{"lark-cli"}, argv...), nil
	case "lark_calendar_agenda":
		cmd := []string{"lark-cli", "calendar", "+agenda", "--as", "user"}
		if value := stringArg(args, "start"); value != "" {
			cmd = append(cmd, "--start", value)
		}
		if value := stringArg(args, "end"); value != "" {
			cmd = append(cmd, "--end", value)
		}
		return cmd, nil
	case "lark_calendar_create":
		cmd := []string{"lark-cli", "calendar", "+create", "--as", "user"}
		if value := stringArg(args, "summary"); value != "" {
			cmd = append(cmd, "--summary", value)
		}
		if value := stringArg(args, "start_time"); value != "" {
			cmd = append(cmd, "--start-time", value)
		}
		if value := stringArg(args, "end_time"); value != "" {
			cmd = append(cmd, "--end-time", value)
		}
		if value := stringArg(args, "description"); value != "" {
			cmd = append(cmd, "--description", value)
		}
		return cmd, nil
	case "lark_im_send":
		cmd := []string{"lark-cli", "im", "+messages-send", "--as", "user"}
		idType := stringArg(args, "receive_id_type")
		if idType == "" {
			idType = "chat_id"
		}
		switch idType {
		case "chat_id":
			cmd = append(cmd, "--chat-id", stringArg(args, "receive_id"))
		case "user_id", "open_id":
			cmd = append(cmd, "--user-id", stringArg(args, "receive_id"))
		case "email":
			cmd = append(cmd, "--user-id", stringArg(args, "receive_id"))
		default:
			return nil, fmt.Errorf("unsupported receive_id_type: %s", idType)
		}
		if msgType := stringArg(args, "msg_type"); msgType == "markdown" {
			cmd = append(cmd, "--markdown", stringArg(args, "text"))
		} else {
			cmd = append(cmd, "--text", stringArg(args, "text"))
		}
		return cmd, nil
	case "lark_im_search":
		cmd := []string{"lark-cli", "im", "+messages-search", "--as", "user", "--query", stringArg(args, "query")}
		if value := stringArg(args, "chat_id"); value != "" {
			cmd = append(cmd, "--chat-id", value)
		}
		return cmd, nil
	case "lark_docs_create":
		cmd := []string{"lark-cli", "docs", "+create", "--api-version", "v2", "--as", "user", "--title", stringArg(args, "title")}
		markdown := stringArg(args, "markdown")
		if strings.TrimSpace(markdown) == "" {
			return nil, fmt.Errorf("markdown 正文不能为空；请先整理内容，再调用 lark_docs_create")
		}
		cmd = append(cmd, "--markdown", markdown)
		if value := stringArg(args, "folder_token"); value != "" {
			cmd = append(cmd, "--folder-token", value)
		}
		return cmd, nil
	case "lark_docs_fetch":
		return []string{"lark-cli", "docs", "+fetch", "--api-version", "v2", "--as", "user", "--doc", stringArg(args, "doc_token")}, nil
	case "lark_base_records":
		action := stringArg(args, "action")
		if action == "" {
			action = "list"
		}
		cmd := []string{"lark-cli", "base", "record", action, "--as", "user", "--app-token", stringArg(args, "app_token"), "--table-id", stringArg(args, "table_id")}
		if action == "list" {
			if value := stringArg(args, "filter"); value != "" {
				cmd = append(cmd, "--filter", value)
			}
		}
		if action == "create" || action == "update" {
			if fields, ok := args["fields"]; ok {
				payload, _ := json.Marshal(fields)
				cmd = append(cmd, "--fields", string(payload))
			}
		}
		if action == "update" {
			if value := stringArg(args, "record_id"); value != "" {
				cmd = append(cmd, "--record-id", value)
			}
		}
		return cmd, nil
	case "lark_sheets_info":
		cmd := []string{"lark-cli", "sheets", "+info", "--as", "user"}
		if value := stringArg(args, "spreadsheet_token"); value != "" {
			cmd = append(cmd, "--spreadsheet-token", value)
		}
		if value := stringArg(args, "url"); value != "" {
			cmd = append(cmd, "--url", value)
		}
		return cmd, nil
	case "lark_sheets_read":
		cmd := []string{"lark-cli", "sheets", "+read", "--as", "user", "--spreadsheet-token", stringArg(args, "spreadsheet_token"), "--range", stringArg(args, "range")}
		if value := stringArg(args, "sheet_id"); value != "" {
			cmd = append(cmd, "--sheet-id", value)
		}
		return cmd, nil
	case "lark_task_list":
		cmd := []string{"lark-cli", "task", "list", "--as", "user"}
		if boolArg(args, "is_completed") {
			cmd = append(cmd, "--completed")
		}
		return cmd, nil
	case "lark_wiki_search":
		action := stringArg(args, "action")
		switch action {
		case "", "list_spaces":
			return []string{"lark-cli", "wiki", "space", "list", "--as", "user"}, nil
		case "list_nodes":
			return []string{"lark-cli", "wiki", "space", "node", "list", "--as", "user", "--space-id", stringArg(args, "space_id")}, nil
		case "get_node":
			return []string{"lark-cli", "wiki", "node", "get", "--as", "user", "--node-token", stringArg(args, "node_token")}, nil
		default:
			return nil, fmt.Errorf("unsupported wiki action: %s", action)
		}
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func executeTool(toolName string, args map[string]any) ToolExecutionResult {
	return executeToolContext(context.Background(), toolName, args)
}

func executeToolContext(parent context.Context, toolName string, args map[string]any) ToolExecutionResult {
	return executeToolContextWithConfig(parent, DefaultConfig(), toolName, args)
}

func executeToolContextWithConfig(parent context.Context, cfg Config, toolName string, args map[string]any) ToolExecutionResult {
	if toolName == "mcp_call" {
		return executeMCPToolContext(parent, cfg, args)
	}
	if toolName == "lark_skill_view" {
		output, err := renderLarkSkillView(stringArg(args, "name"))
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: output}
	}
	cmdArgs, err := buildToolCommand(toolName, args)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	if isLarkAuthLoginCommand(cmdArgs) {
		return ToolExecutionResult{
			Output:     "[error] auth login 由 Feishu Bridge 登录流程接管；已请求 Bridge 发起授权并返回授权链接。",
			NeedsLogin: true,
			IsError:    true,
		}
	}
	ctx, cancel := context.WithTimeout(parent, toolTimeout())
	defer cancel()
	cmd := commandContextWithEnv(ctx, cmdArgs[0], cmdArgs[1:]...)
	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(decodeCommandOutput(output))
	perm := parsePermissionRequirement(result)
	result = normalizeToolOutput(result)
	if result == "" && err == nil {
		result = "[no output]"
	}
	if len(result) > 4000 {
		result = result[:4000] + "\n... (truncated; do not infer unseen content)"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ToolExecutionResult{Output: "[error] command timed out (30s)", IsError: true}
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && result != "" {
			return ToolExecutionResult{
				Output:        fmt.Sprintf("[error] %s (exit=%d)", result, exitErr.ExitCode()),
				MissingScopes: perm.Scopes,
				NeedsLogin:    perm.NeedsLogin,
				ConsoleURL:    perm.ConsoleURL,
				Hint:          perm.Hint,
				IsError:       true,
			}
		}
		return ToolExecutionResult{
			Output:        "[error] " + err.Error(),
			MissingScopes: perm.Scopes,
			NeedsLogin:    perm.NeedsLogin,
			ConsoleURL:    perm.ConsoleURL,
			Hint:          perm.Hint,
			IsError:       true,
		}
	}
	return ToolExecutionResult{Output: result}
}

func executeToolContextWithRuntime(parent context.Context, cfg Config, runtime *MCPRuntime, toolName string, args map[string]any) ToolExecutionResult {
	if runtime != nil && runtime.IsMCPFunction(toolName) {
		if !cfg.MCPEnabled {
			return ToolExecutionResult{Output: "[error] MCP 未启用", IsError: true}
		}
		ctx, cancel := context.WithTimeout(parent, toolTimeout())
		defer cancel()
		result, err := runtime.CallTool(ctx, toolName, args)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		result = strings.TrimSpace(result)
		if result == "" {
			result = "[no output]"
		}
		if len(result) > 4000 {
			result = result[:4000] + "\n... (truncated; do not infer unseen content)"
		}
		return ToolExecutionResult{Output: result}
	}
	return executeToolContextWithConfig(parent, cfg, toolName, args)
}

func normalizeToolOutput(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return result
	}
	if summary := summarizeDriveSearchResult(result); summary != "" {
		return summary
	}
	if summary := summarizeChatListResult(result); summary != "" {
		return summary
	}
	return result
}

func summarizeDriveSearchResult(result string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return ""
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return ""
	}
	rawResults, ok := data["results"].([]any)
	if !ok {
		return ""
	}
	type driveSearchItem struct {
		Title     string `json:"title"`
		Entity    string `json:"entity_type,omitempty"`
		DocType   string `json:"doc_type,omitempty"`
		URL       string `json:"url,omitempty"`
		Token     string `json:"token,omitempty"`
		Owner     string `json:"owner,omitempty"`
		UpdatedAt string `json:"updated_at,omitempty"`
	}
	items := make([]driveSearchItem, 0, len(rawResults))
	for _, raw := range rawResults {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		meta, _ := item["result_meta"].(map[string]any)
		if meta == nil {
			continue
		}
		title := strings.TrimSpace(fmt.Sprint(item["title_highlighted"]))
		if title == "" || title == "<nil>" {
			title = strings.TrimSpace(fmt.Sprint(item["title"]))
		}
		url := strings.TrimSpace(fmt.Sprint(meta["url"]))
		if url == "" || url == "<nil>" {
			continue
		}
		items = append(items, driveSearchItem{
			Title:     title,
			Entity:    strings.TrimSpace(fmt.Sprint(item["entity_type"])),
			DocType:   strings.TrimSpace(fmt.Sprint(meta["doc_types"])),
			URL:       url,
			Token:     strings.TrimSpace(fmt.Sprint(meta["token"])),
			Owner:     strings.TrimSpace(fmt.Sprint(meta["owner_name"])),
			UpdatedAt: strings.TrimSpace(fmt.Sprint(meta["update_time_iso"])),
		})
	}
	if len(items) == 0 {
		return ""
	}
	total := intFromAny(data["total"])
	hasMore, _ := data["has_more"].(bool)
	out := map[string]any{
		"ok":         payload["ok"],
		"identity":   payload["identity"],
		"kind":       "drive_search",
		"summary":    fmt.Sprintf("当前页返回 %d 个真实云盘结果；total=%d；has_more=%v。回答时只能复制 results[].url，禁止自造链接。", len(items), total, hasMore),
		"has_more":   hasMore,
		"page_token": strings.TrimSpace(fmt.Sprint(data["page_token"])),
		"results":    items,
		"total":      total,
	}
	if out["page_token"] == "<nil>" {
		out["page_token"] = ""
	}
	text, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ""
	}
	return string(text)
}

func summarizeChatListResult(result string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return ""
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return ""
	}
	rawChats, ok := data["chats"].([]any)
	if !ok {
		return ""
	}
	if len(rawChats) == 0 {
		return "{\n  \"ok\": true,\n  \"identity\": \"user\",\n  \"summary\": \"当前没有可列出的会话\",\n  \"chats\": []\n}"
	}
	type chatSummary struct {
		Name      string `json:"name"`
		ChatID    string `json:"chat_id"`
		Scope     string `json:"scope"`
		ChatScope string `json:"chat_scope"`
	}
	summaries := make([]chatSummary, 0, len(rawChats))
	for _, raw := range rawChats {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		name := strings.TrimSpace(fmt.Sprint(item["name"]))
		if name == "" || name == "<nil>" {
			name = "(未命名会话)"
		}
		isExternal := strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["external"])), "true")
		scope := "内部会话"
		if isExternal {
			scope = "外部聊天"
		}
		summaries = append(summaries, chatSummary{
			Name:      name,
			ChatID:    strings.TrimSpace(fmt.Sprint(item["chat_id"])),
			Scope:     scope,
			ChatScope: map[bool]string{true: "external", false: "internal"}[isExternal],
		})
	}
	if len(summaries) == 0 {
		return ""
	}
	limit := 20
	if len(summaries) < limit {
		limit = len(summaries)
	}
	out := map[string]any{
		"ok":       payload["ok"],
		"identity": payload["identity"],
		"summary":  fmt.Sprintf("当前返回 %d 个会话；以下仅列出前 %d 个真实结果，请勿推断未展示项。", len(summaries), limit),
		"chats":    summaries[:limit],
	}
	text, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ""
	}
	return string(text)
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func stringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func boolArg(args map[string]any, key string) bool {
	value, ok := args[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func stringListArg(args map[string]any, key string) []string {
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, item)
			}
		}
		return items
	case []any:
		items := make([]string, 0, len(typed))
		for _, raw := range typed {
			item := strings.TrimSpace(fmt.Sprint(raw))
			if item != "" {
				items = append(items, item)
			}
		}
		return items
	default:
		return nil
	}
}

func normalizeLarkCLIArgv(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	out := append([]string(nil), argv...)
	lower := make([]string, len(out))
	for i, item := range out {
		lower[i] = strings.ToLower(strings.TrimSpace(item))
	}
	replace := func(n int, shortcut string) []string {
		next := make([]string, 0, 1+len(out)-n)
		next = append(next, out[0], shortcut)
		next = append(next, out[n:]...)
		return next
	}
	if len(out) >= 3 {
		switch lower[0] {
		case "im":
			switch {
			case (lower[1] == "chat" || lower[1] == "chats") && lower[2] == "list":
				return replace(3, "+chat-list")
			case lower[1] == "chat" && lower[2] == "create":
				return replace(3, "+chat-create")
			case (lower[1] == "message" || lower[1] == "messages") && lower[2] == "send":
				return replace(3, "+messages-send")
			case (lower[1] == "message" || lower[1] == "messages") && lower[2] == "search":
				return replace(3, "+messages-search")
			case (lower[1] == "message" || lower[1] == "messages") && lower[2] == "reply":
				return replace(3, "+messages-reply")
			}
		case "calendar":
			switch {
			case lower[1] == "agenda":
				return replace(2, "+agenda")
			case lower[1] == "create":
				return replace(2, "+create")
			}
		case "docs":
			switch {
			case lower[1] == "create":
				return replace(2, "+create")
			case lower[1] == "fetch":
				return replace(2, "+fetch")
			}
		case "sheets":
			switch {
			case lower[1] == "cell" && lower[2] == "read":
				return replace(3, "+read")
			case lower[1] == "spreadsheets" && lower[2] == "get":
				return replace(3, "+info")
			}
		}
	}
	if len(out) >= 2 {
		switch lower[0] {
		case "calendar":
			if lower[1] == "agenda" || lower[1] == "create" {
				return replace(2, "+"+lower[1])
			}
		case "docs":
			if lower[1] == "create" || lower[1] == "fetch" {
				return replace(2, "+"+lower[1])
			}
		case "sheets":
			if lower[1] == "info" || lower[1] == "read" {
				return replace(2, "+"+lower[1])
			}
		}
	}
	return out
}

func containsAsFlag(argv []string) bool {
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--as" && i+1 < len(argv) {
			return true
		}
		if strings.HasPrefix(argv[i], "--as=") {
			return true
		}
	}
	return false
}

func shouldAutoAppendAs(argv []string) bool {
	if len(argv) == 0 || isLarkAuthArgv(argv) || isLarkHelpArgv(argv) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(argv[0])) {
	case "approval",
		"attendance",
		"base",
		"calendar",
		"contact",
		"docs",
		"drive",
		"im",
		"mail",
		"minutes",
		"okr",
		"sheets",
		"slides",
		"task",
		"vc",
		"whiteboard",
		"wiki":
		return true
	default:
		return false
	}
}

func isLarkAuthArgv(argv []string) bool {
	if len(argv) < 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(argv[0]), "auth")
}

func isLarkAuthLoginArgv(argv []string) bool {
	return len(argv) >= 2 &&
		isLarkAuthArgv(argv) &&
		strings.EqualFold(strings.TrimSpace(argv[1]), "login")
}

func isLarkHelpArgv(argv []string) bool {
	for _, arg := range argv {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "--help" || trimmed == "-h" || trimmed == "help" {
			return true
		}
	}
	return false
}

func isLarkAuthLoginCommand(cmdArgs []string) bool {
	if len(cmdArgs) < 3 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cmdArgs[0]), "lark-cli") &&
		isLarkAuthLoginArgv(cmdArgs[1:])
}

func parsePermissionRequirement(text string) permissionRequirement {
	text = strings.TrimSpace(text)
	if text == "" {
		return permissionRequirement{}
	}
	requirement := permissionRequirement{}
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "need_user_authorization") ||
		strings.Contains(lowerText, "requires user authorization") ||
		strings.Contains(lowerText, "current command requires user authorization") {
		requirement.NeedsLogin = true
	}

	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) == nil {
		requirement.Scopes = append(requirement.Scopes, extractScopesFromValue(payload["permission_violations"])...)
		if value, ok := payload["console_url"].(string); ok {
			requirement.ConsoleURL = strings.TrimSpace(value)
		}
		if value, ok := payload["hint"].(string); ok {
			requirement.Hint = strings.TrimSpace(value)
		}
		if errValue, ok := payload["error"].(map[string]any); ok {
			requirement.Scopes = append(requirement.Scopes, extractScopesFromValue(errValue["permission_violations"])...)
			if value, ok := errValue["console_url"].(string); ok && requirement.ConsoleURL == "" {
				requirement.ConsoleURL = strings.TrimSpace(value)
			}
			if value, ok := errValue["hint"].(string); ok && requirement.Hint == "" {
				requirement.Hint = strings.TrimSpace(value)
			}
			if value, ok := errValue["message"].(string); ok && len(requirement.Scopes) == 0 {
				requirement.Scopes = append(requirement.Scopes, extractScopesFromHint(value)...)
				if strings.Contains(strings.ToLower(value), "need_user_authorization") ||
					strings.Contains(strings.ToLower(value), "requires user authorization") {
					requirement.NeedsLogin = true
				}
			}
		}
	}

	if requirement.Hint == "" {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "auth login --scope") {
				requirement.Hint = line
				break
			}
		}
	}
	if len(requirement.Scopes) == 0 && requirement.Hint != "" {
		requirement.Scopes = append(requirement.Scopes, extractScopesFromHint(requirement.Hint)...)
	}
	requirement.Scopes = uniqueNonEmpty(requirement.Scopes)
	return requirement
}

func extractScopesFromHint(text string) []string {
	if matches := scopeHintPattern.FindStringSubmatch(text); len(matches) == 2 {
		return []string{strings.TrimSpace(matches[1])}
	}
	const marker = "missing required scope(s):"
	lower := strings.ToLower(text)
	idx := strings.Index(lower, marker)
	if idx == -1 {
		return nil
	}
	scopeText := strings.TrimSpace(text[idx+len(marker):])
	scopeText = strings.Trim(scopeText, " .。")
	if scopeText == "" {
		return nil
	}
	parts := strings.FieldsFunc(scopeText, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；'
	})
	return uniqueNonEmpty(parts)
}

func extractScopesFromValue(value any) []string {
	switch typed := value.(type) {
	case []any:
		var scopes []string
		for _, item := range typed {
			scopes = append(scopes, extractScopesFromValue(item)...)
		}
		return scopes
	case map[string]any:
		var scopes []string
		if raw, ok := typed["scope"]; ok {
			if scope := strings.TrimSpace(fmt.Sprint(raw)); scope != "" && scope != "<nil>" {
				scopes = append(scopes, scope)
			}
		}
		if raw, ok := typed["scopes"]; ok {
			scopes = append(scopes, extractScopesFromValue(raw)...)
		}
		return scopes
	case string:
		scope := strings.TrimSpace(typed)
		if scope == "" {
			return nil
		}
		return []string{scope}
	default:
		return nil
	}
}

func uniqueNonEmpty(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func toolTimeout() time.Duration {
	return 30 * time.Second
}
