package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

func toolDefinitions() []map[string]any {
	return []map[string]any{
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
				"description": "创建飞书云文档（docx 格式）。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":        map[string]any{"type": "string", "description": "文档标题"},
						"folder_token": map[string]any{"type": "string", "description": "存放文件夹的 token（可选，默认根目录）"},
					},
					"required": []string{"title"},
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
				"name":        "lark_sheets_read",
				"description": "读取飞书电子表格单元格数据。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spreadsheet_token": map[string]any{"type": "string", "description": "表格 token"},
						"range":             map[string]any{"type": "string", "description": "读取范围，如 Sheet1!A1:D10"},
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

func buildToolCommand(toolName string, args map[string]any) ([]string, error) {
	switch toolName {
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
	case "lark_sheets_read":
		return []string{"lark-cli", "sheets", "cell", "read", "--as", "user", "--spreadsheet-token", stringArg(args, "spreadsheet_token"), "--range", stringArg(args, "range")}, nil
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

func executeTool(toolName string, args map[string]any) string {
	cmdArgs, err := buildToolCommand(toolName, args)
	if err != nil {
		return "[error] " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(output))
	if result == "" && err == nil {
		result = "[no output]"
	}
	if len(result) > 4000 {
		result = result[:4000] + "\n... (truncated)"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "[error] command timed out (30s)"
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && result != "" {
			return fmt.Sprintf("[error] %s (exit=%d)", result, exitErr.ExitCode())
		}
		return "[error] " + err.Error()
	}
	return result
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

func toolTimeout() time.Duration {
	return 30 * time.Second
}
