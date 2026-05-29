package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
				"description": "通用飞书 CLI 执行入口。适用于当前未单独结构化建模的 lark-cli 能力。传入 argv 数组，不要包含程序名 lark-cli；已知飞书业务域命令如果未显式指定 --as，则会自动追加 --as user；auth、--help/-h 和未知根命令不会追加 --as。\n\n强制规则：\n- 如果准备使用某个业务域但不确定命令/参数，先调用 lark_skill_view 阅读对应官方 Skill（如 lark-sheets、lark-doc、lark-drive），再执行本工具；不要凭经验猜 Sheet1/0/1、docs/file/list 等命令\n- 本工具返回 Usage/unknown flag/invalid value 后，下一步必须先查 lark_skill_view、--help 或 schema，不能原样重复或换相似命令盲试\n- 授权不要通过本工具执行 auth login；遇到 need_user_authorization 时 Agent 会自动发起登录并返回授权链接\n- 查询当前身份优先用 argv：[\"auth\", \"status\"]；查看已登录用户用 argv：[\"auth\", \"list\"]\n- 设置文档互联网公开权限使用 drive permission.public get/patch；不要使用 drive +apply-permission，它只申请 view/edit 权限\n- lark-cli 的子命令分两类：原生子命令（如 drive file list）和 skill 快捷命令（带 + 前缀，如 im +chat-list、im +messages-send）\n- IM 相关操作必须使用 + 前缀快捷命令：im +chat-list、im +chat-create、im +messages-send、im +messages-search、im +messages-reply\n- 示例 argv：[\"im\", \"+chat-list\", \"--limit\", \"10\"]、[\"drive\", \"permission.public\", \"get\", \"--params\", \"{...}\"]、[\"calendar\", \"+agenda\"]",
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
				"description": "读取本机已安装的官方飞书 lark-cli Skill 文档。支持分页：如果结果中 agent_reading.has_more=true，必须用 next_offset 继续调用，直到 has_more=false。用于在执行 lark_cli_exec 前确认真实命令、shortcut、参数和注意事项。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "description": "官方 Skill 名称或业务域，例如 lark-sheets、sheets、docs、drive"},
						"offset":      map[string]any{"type": "integer", "description": "正文分块读取起点，首次读取填 0 或省略；继续读取使用上次返回的 agent_reading.next_offset"},
						"chunk_size":  map[string]any{"type": "integer", "description": "本次读取的正文字符数，默认 6000，最大 12000"},
						"require_all": map[string]any{"type": "boolean", "description": "当用户要求完整阅读时设为 true"},
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
				"name":        "web_search",
				"description": "搜索公开互联网信息，适合最新资讯、网页资料、天气以外的一般事实查询。返回搜索结果摘要和来源链接。不要用它查询飞书内部数据；飞书内部数据优先用 lark-cli 工具。",
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
				"name":        "web_fetch",
				"description": "读取公开 HTTP/HTTPS URL 内容。适合抓取网页、公开 JSON 或 Markdown。默认只做 GET；不要用它访问飞书内部云文档，飞书文档优先用 lark_docs_fetch。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{"type": "string", "description": "完整 HTTP/HTTPS URL"},
					},
					"required": []string{"url"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "weather_lookup",
				"description": "查询公开天气信息。需要用户给出城市/地区；如果用户只说“天气”但没有地点，应先追问地点。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string", "description": "城市或地区，例如 广州、深圳、北京、Shanghai"},
					},
					"required": []string{"location"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "schedule_task",
				"description": "创建、查看和管理 Feishu Agent 定时任务。仅当用户明确要求提醒、稍后执行、每天/每周/定期检查、持续监控时使用；普通飞书操作不要创建定时任务。定时任务到点后会自动执行 prompt 并投递到当前飞书聊天，任务内部最终回复会自动发送，不要再调用 lark_im_send 自行发送。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string", "enum": []string{"create", "list", "delete", "pause", "resume", "run_now"}, "description": "操作类型"},
						"task_id": map[string]any{
							"type":        "string",
							"description": "已有任务 ID。delete/pause/resume/run_now 时必填",
						},
						"name": map[string]any{"type": "string", "description": "任务名称，create 时可选"},
						"prompt": map[string]any{
							"type":        "string",
							"description": "到点后要执行的完整任务描述，create 时必填。写成自包含指令，不要写“提醒我”这种缺少内容的片段。",
						},
						"schedule_kind": map[string]any{"type": "string", "enum": []string{"at", "every"}, "description": "at=一次性；every=固定间隔循环"},
						"at":            map[string]any{"type": "string", "description": "首次/一次性执行时间，推荐 RFC3339，也支持 YYYY-MM-DD HH:mm"},
						"delay_seconds": map[string]any{"type": "integer", "description": "从现在开始延迟多少秒后执行一次"},
						"every_seconds": map[string]any{"type": "integer", "description": "循环间隔秒数，至少 60。每天=86400，每周=604800"},
						"timezone":      map[string]any{"type": "string", "description": "时区，默认 Asia/Shanghai"},
						"delete_after_run": map[string]any{
							"type":        "boolean",
							"description": "一次性任务执行后是否删除/停用，默认停用",
						},
					},
					"required": []string{"action"},
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
				"description": "读取飞书云文档内容。通过文档 URL 或 token 获取正文。长文档会按正文字符分块返回：如果结果里的 agent_reading.has_more=true，必须用 next_offset 继续调用，直到 has_more=false 后才能声称已完整阅读全文。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"doc_token":   map[string]any{"type": "string", "description": "文档 token 或完整 URL"},
						"offset":      map[string]any{"type": "integer", "description": "正文分块读取起点，首次读取填 0 或省略；继续读取使用上次返回的 agent_reading.next_offset"},
						"chunk_size":  map[string]any{"type": "integer", "description": "本次读取的正文字符数，默认 6000，最大 12000"},
						"require_all": map[string]any{"type": "boolean", "description": "当用户要求全文/整篇/完整阅读时设为 true；工具会在结果中强化续读提示"},
					},
					"required": []string{"doc_token"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_drive_search",
				"description": "搜索飞书云盘文件/文档，返回真实标题、token、URL、total、has_more 和 page_token。需要完整列表时必须按 page_token 继续分页；回答只能引用结果里的真实 URL，禁止自造链接。类型过滤用 doc_types，例如 docx、sheet、bitable、wiki。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":       map[string]any{"type": "string", "description": "搜索关键词，可选；不要超过飞书接口限制，长标题应拆成短关键词"},
						"doc_types":   map[string]any{"type": "string", "description": "逗号分隔类型，例如 docx,sheet,bitable,wiki,file,folder"},
						"mine":        map[string]any{"type": "boolean", "description": "只查我拥有/我相关的文档"},
						"creator_ids": map[string]any{"type": "string", "description": "逗号分隔 owner open_id；与 mine 二选一"},
						"page_token":  map[string]any{"type": "string", "description": "上一页返回的 page_token，用于继续分页"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lark_permission_public",
				"description": "查看或设置飞书文档的链接公开权限。用于“互联网所有人可见/所有人可阅读/公开访问”。不要用 drive +apply-permission；它只能申请 view/edit，不能改公开权限。设置后必须再次 get 验证。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":            map[string]any{"type": "string", "enum": []string{"get", "patch"}, "description": "get 查看当前公开权限；patch 修改公开权限"},
						"token":             map[string]any{"type": "string", "description": "文档 token 或 URL"},
						"type":              map[string]any{"type": "string", "enum": []string{"doc", "docx", "sheet", "bitable", "wiki", "file", "mindnote", "slides"}, "description": "文档类型；docx 云文档填 docx"},
						"external_access":   map[string]any{"type": "boolean", "description": "是否允许组织外访问；互联网公开一般为 true"},
						"link_share_entity": map[string]any{"type": "string", "description": "链接分享实体；互联网所有人可见一般用 anyone_readable"},
					},
					"required": []string{"action", "token", "type"},
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
		{
			"type": "function",
			"function": map[string]any{
				"name":        "safe_file_read",
				"description": "读取本地文本文件内容。只支持 Feishu Agent 高级设置允许的路径，或用户最新消息通过“授权目录/授权文件 <绝对路径>”授予的只读路径。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "文件的绝对路径"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "safe_file_write",
				"description": "安全写入本地文本文件。只允许已授权路径；创建新文件默认允许，覆盖已有文件必须 overwrite=true 且用户最新消息明确说“确认覆盖 <文件名或绝对路径>”。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":      map[string]any{"type": "string", "description": "目标文件绝对路径"},
						"content":   map[string]any{"type": "string", "description": "要写入的完整文本内容"},
						"overwrite": map[string]any{"type": "boolean", "description": "是否覆盖已有文件。覆盖时必须有用户最新消息的精确确认。"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "safe_file_list",
				"description": "列出本地已授权路径下的文件和子目录列表。只能列出高级设置允许的路径，或用户口述授权的只读路径。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "需要列出的目录绝对路径"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "authorize_local_path",
				"description": "向白名单添加本地文件或目录。只能在用户最新消息明确包含“授权目录 <绝对路径>”或“授权文件 <绝对路径>”时调用；模型不能自行授权。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "需要授权的绝对路径，例如 /Users/tiancheng/my-project"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "list_authorized_paths",
				"description": "列出当前已授权的所有本地路径白名单。",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "safe_file_delete",
				"description": "安全删除本地文件。操作的路径必须在已授权的白名单内。执行该操作必须先向用户申请二次确认，只有当用户在最新的对话中回复了‘确认删除 [文件名]’时，才可以且必须将 confirmed 设为 true 后再次执行删除。禁止擅自绕过二次确认直接删除。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":      map[string]any{"type": "string", "description": "要删除的文件绝对路径"},
						"confirmed": map[string]any{"type": "boolean", "description": "必须设为 true 以声明已获得用户口头二次验证。默认或未确认时设为 false。"},
					},
					"required": []string{"path", "confirmed"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "mcp_resource_read",
				"description": "读取 MCP 服务器提供的资源内容。传入资源 URI（从系统提示词中的 MCP 资源列表获取），返回资源的文本内容。适用于读取文件、数据库记录、API 响应等 MCP 暴露的数据源。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"uri": map[string]any{"type": "string", "description": "资源的 URI，例如 file:///project/README.md"},
					},
					"required": []string{"uri"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "mcp_prompt_get",
				"description": "获取 MCP 服务器提供的提示词模板内容。传入提示词名称（格式 server:name）和可选参数，返回结构化消息。适用于调用 MCP 服务器预定义的提示词模板。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":      map[string]any{"type": "string", "description": "提示词名称，格式 server:name，从系统提示词中的 MCP 提示词列表获取"},
						"arguments": map[string]any{"type": "object", "description": "提示词参数（可选），键值对格式"},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "feishu_history_search",
				"description": "搜索当前飞书群聊的历史消息。用于查找之前讨论过的话题、提到的文档/链接、用户说过的话等。这是飞书聊天记录的全文搜索，可以找到上下文窗口之外的历史内容。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "搜索关键词"},
						"limit": map[string]any{"type": "integer", "description": "返回结果数量上限，默认 10"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "fetch_tool_memory",
				"description": "检索之前工具调用的完整结果。当上下文中的工具结果被压缩、你需要回顾之前获取的详细数据时使用。支持按关键词搜索或按 ID 精确获取。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"search", "get"},
							"description": "search=按关键词搜索；get=按 ID 精确获取",
						},
						"query":     map[string]any{"type": "string", "description": "搜索关键词（action=search 时必填）"},
						"memory_id": map[string]any{"type": "string", "description": "工具记忆 ID（action=get 时必填，如 tool_abc123）"},
						"limit":     map[string]any{"type": "integer", "description": "搜索结果数量上限，默认 5"},
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
				"description": "列出用户在 Feishu Agent 高级设置中导入并启用的 Skills。只返回索引，不返回完整正文。",
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
				"description": "读取一个已启用 Skill 的 SKILL.md 正文。支持分页：如果结果中 agent_reading.has_more=true，必须用 next_offset 继续调用，直到 has_more=false 后才能声称已完整阅读。只有准备使用该 skill 时再调用，避免浪费上下文。",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "description": "Skill 名称或 ID"},
						"offset":      map[string]any{"type": "integer", "description": "正文分块读取起点，首次读取填 0 或省略；继续读取使用上次返回的 agent_reading.next_offset"},
						"chunk_size":  map[string]any{"type": "integer", "description": "本次读取的正文字符数，默认 6000，最大 12000"},
						"require_all": map[string]any{"type": "boolean", "description": "当用户要求全文/完整阅读时设为 true；工具会在结果中强化续读提示"},
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

func isAgentSkillTool(name string) bool {
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
	case "lark_drive_search":
		cmd := []string{"lark-cli", "drive", "+search", "--as", "user", "--format", "json"}
		if value := stringArg(args, "query"); value != "" {
			cmd = append(cmd, "--query", value)
		}
		if value := stringArg(args, "doc_types"); value != "" {
			cmd = append(cmd, "--doc-types", value)
		}
		if boolArg(args, "mine") {
			cmd = append(cmd, "--mine")
		} else if value := stringArg(args, "creator_ids"); value != "" {
			cmd = append(cmd, "--creator-ids", value)
		}
		if value := stringArg(args, "page_token"); value != "" {
			cmd = append(cmd, "--page-token", value)
		}
		return cmd, nil
	case "lark_permission_public":
		action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
		if action == "" {
			action = "get"
		}
		if action != "get" && action != "patch" {
			return nil, fmt.Errorf("unsupported permission action: %s", action)
		}
		token := stringArg(args, "token")
		docType := stringArg(args, "type")
		if strings.TrimSpace(token) == "" || strings.TrimSpace(docType) == "" {
			return nil, fmt.Errorf("token 和 type 不能为空")
		}
		params, _ := json.Marshal(map[string]any{"token": token, "type": docType})
		cmd := []string{"lark-cli", "drive", "permission.public", action, "--as", "user", "--params", string(params)}
		if action == "patch" {
			externalAccess := true
			if value, ok := args["external_access"].(bool); ok {
				externalAccess = value
			}
			entity := stringArg(args, "link_share_entity")
			if entity == "" {
				entity = "anyone_readable"
			}
			data, _ := json.Marshal(map[string]any{
				"external_access":   externalAccess,
				"link_share_entity": entity,
			})
			cmd = append(cmd, "--data", string(data), "--yes")
		}
		return cmd, nil
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
	switch toolName {
	case "web_search", "web_fetch", "weather_lookup":
		return executeBuiltinWebTool(parent, cfg, toolName, args)
	case "safe_file_read", "safe_file_write", "safe_file_list", "safe_file_delete", "authorize_local_path", "list_authorized_paths":
		return executeSafeFileTool(parent, cfg, toolName, args)
	}
	if toolName == "lark_skill_view" {
		output, err := renderLarkSkillView(stringArg(args, "name"), args)
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
			Output:     "[error] auth login 由 Feishu Agent 登录流程接管；已请求 Agent 发起授权并返回授权链接。",
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
	if toolName == "lark_docs_fetch" {
		if chunked, ok := chunkLarkDocsFetchResult(result, args); ok {
			result = chunked
		}
	}
	if toolName == "lark_sheets_read" {
		if chunked, ok := chunkLarkSheetsReadResult(result, args); ok {
			result = chunked
		}
	}
	if result == "" && err == nil {
		result = "[no output]"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ToolExecutionResult{Output: "[error] command timed out (30s)", IsError: true}
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && result != "" {
			result = appendLarkCLICorrection(cmdArgs, result)
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

const (
	defaultDocsFetchChunkChars = 6000
	maxDocsFetchChunkChars     = 12000
	minDocsFetchChunkChars     = 1000
	defaultSheetsReadMaxRows   = 80
)

func chunkLarkDocsFetchResult(result string, args map[string]any) (string, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return "", false
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return "", false
	}
	document, _ := data["document"].(map[string]any)
	if document == nil {
		return "", false
	}
	content, ok := document["content"].(string)
	if !ok {
		return "", false
	}
	runes := []rune(content)
	total := len(runes)
	offset := intFromAny(args["offset"])
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	chunkSize := intFromAny(args["chunk_size"])
	if chunkSize <= 0 {
		chunkSize = defaultDocsFetchChunkChars
	}
	if chunkSize < minDocsFetchChunkChars {
		chunkSize = minDocsFetchChunkChars
	}
	if chunkSize > maxDocsFetchChunkChars {
		chunkSize = maxDocsFetchChunkChars
	}
	end := offset + chunkSize
	if end > total {
		end = total
	}
	document["content"] = string(runes[offset:end])
	hasMore := end < total
	reading := map[string]any{
		"kind":            "doc_content_chunk",
		"offset":          offset,
		"end_offset":      end,
		"next_offset":     end,
		"chunk_chars":     end - offset,
		"total_chars":     total,
		"has_more":        hasMore,
		"complete":        !hasMore,
		"instruction":     "如果用户要求全文、整篇、完整阅读、全文总结或按全文改写，必须继续调用 lark_docs_fetch，并把 offset 设为 next_offset，直到 has_more=false。禁止基于当前分块推断未读取内容。",
		"chunk_size_hint": fmt.Sprintf("继续读取建议使用 chunk_size=%d", chunkSize),
	}
	if boolArg(args, "require_all") && hasMore {
		reading["required_next_call"] = map[string]any{
			"tool":       "lark_docs_fetch",
			"doc_token":  stringArg(args, "doc_token"),
			"offset":     end,
			"chunk_size": chunkSize,
		}
	}
	data["agent_reading"] = reading
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", false
	}
	return string(text), true
}

func chunkLarkSheetsReadResult(result string, args map[string]any) (string, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return "", false
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return "", false
	}
	valueRange, _ := data["valueRange"].(map[string]any)
	if valueRange == nil {
		return "", false
	}
	values, ok := valueRange["values"].([]any)
	if !ok || len(values) <= defaultSheetsReadMaxRows {
		return "", false
	}
	totalRows := len(values)
	valueRange["values"] = values[:defaultSheetsReadMaxRows]
	nextRange := nextA1Range(stringArg(args, "range"), defaultSheetsReadMaxRows)
	reading := map[string]any{
		"kind":          "sheet_rows_chunk",
		"included_rows": defaultSheetsReadMaxRows,
		"total_rows":    totalRows,
		"has_more":      true,
		"instruction":   "当前只返回了本次范围的前若干行。若用户要求完整表格、完整总结或全部内容，必须继续调用 lark_sheets_read 读取后续 range，禁止基于当前分块推断未读取行。",
	}
	if nextRange != "" {
		reading["next_range"] = nextRange
		reading["required_next_call"] = map[string]any{
			"tool":              "lark_sheets_read",
			"spreadsheet_token": stringArg(args, "spreadsheet_token"),
			"sheet_id":          stringArg(args, "sheet_id"),
			"range":             nextRange,
		}
	}
	data["agent_reading"] = reading
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", false
	}
	return string(text), true
}

var a1RangePattern = regexp.MustCompile(`^([A-Za-z]+)(\d+):([A-Za-z]+)(\d+)$`)

func nextA1Range(input string, consumedRows int) string {
	input = strings.TrimSpace(input)
	if input == "" || consumedRows <= 0 {
		return ""
	}
	prefix := ""
	body := input
	if idx := strings.LastIndex(body, "!"); idx >= 0 {
		prefix = body[:idx+1]
		body = body[idx+1:]
	}
	match := a1RangePattern.FindStringSubmatch(body)
	if len(match) != 5 {
		return ""
	}
	startRow := intFromString(match[2])
	endRow := intFromString(match[4])
	if startRow <= 0 || endRow <= 0 || endRow < startRow {
		return ""
	}
	nextStart := startRow + consumedRows
	if nextStart > endRow {
		return ""
	}
	nextEnd := nextStart + consumedRows - 1
	if nextEnd > endRow {
		nextEnd = endRow
	}
	return fmt.Sprintf("%s%s%d:%s%d", prefix, match[1], nextStart, match[3], nextEnd)
}

func intFromString(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func executeBuiltinWebTool(parent context.Context, cfg Config, toolName string, args map[string]any) ToolExecutionResult {
	cfg.Context = normalizeContextConfig(cfg.Context)
	timeout := time.Duration(cfg.Context.SkillHTTPTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	maxBytes := int64(cfg.Context.SkillHTTPMaxBytes)
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	switch toolName {
	case "web_search":
		query := strings.TrimSpace(stringArg(args, "query"))
		if query == "" {
			return ToolExecutionResult{Output: "[error] query 不能为空", IsError: true}
		}
		searchURL := "https://s.jina.ai/?q=" + url.QueryEscape(query)
		return executeHTTPGet(parent, searchURL, timeout, maxBytes, "web_search")
	case "web_fetch":
		target := strings.TrimSpace(stringArg(args, "url"))
		if target == "" {
			return ToolExecutionResult{Output: "[error] url 不能为空", IsError: true}
		}
		return executeHTTPGet(parent, target, timeout, maxBytes, "web_fetch")
	case "weather_lookup":
		location := strings.TrimSpace(stringArg(args, "location"))
		if location == "" {
			return ToolExecutionResult{Output: "[error] location 不能为空；请先向用户确认城市或地区。", IsError: true}
		}
		weatherURL := "https://wttr.in/" + url.PathEscape(location) + "?format=j1"
		return executeHTTPGet(parent, weatherURL, timeout, maxBytes, "weather_lookup")
	default:
		return ToolExecutionResult{Output: "[error] unknown web tool: " + toolName, IsError: true}
	}
}

func executeHTTPGet(parent context.Context, target string, timeout time.Duration, maxBytes int64, kind string) ToolExecutionResult {
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil {
		return ToolExecutionResult{Output: "[error] invalid URL: " + target, IsError: true}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ToolExecutionResult{Output: "[error] only http/https URLs are allowed", IsError: true}
	}
	if parsed.Host == "" {
		return ToolExecutionResult{Output: "[error] invalid URL: " + target, IsError: true}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	req.Header.Set("User-Agent", "Lingma-Feishu-Agent/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	truncated := int64(len(body)) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	text := strings.TrimSpace(decodeCommandOutput(body))
	if text == "" {
		text = "[empty response]"
	}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		text = stripHTMLForTool(text)
	}
	out := map[string]any{
		"ok":           resp.StatusCode >= 200 && resp.StatusCode < 300,
		"kind":         kind,
		"status":       resp.Status,
		"url":          parsed.String(),
		"content_type": contentType,
		"truncated":    truncated,
		"content":      text,
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return ToolExecutionResult{Output: string(data), IsError: resp.StatusCode < 200 || resp.StatusCode >= 300}
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]+>`)

func stripHTMLForTool(text string) string {
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func appendLarkCLICorrection(cmdArgs []string, result string) string {
	if len(cmdArgs) < 2 || cmdArgs[0] != "lark-cli" {
		return result
	}
	argv := cmdArgs[1:]
	lower := strings.ToLower(result)
	hints := []string{}
	if len(argv) >= 2 && argv[0] == "drive" && argv[1] == "file" && strings.Contains(lower, "available commands") {
		hints = append(hints, "纠偏：云盘搜索/列文件优先先调用 lark_skill_view {\"name\":\"lark-drive\"}，再使用 drive +search；不要继续尝试 drive file list。")
	}
	if len(argv) >= 2 && argv[0] == "drive" && argv[1] == "+search" && containsArg(argv, "--limit") {
		hints = append(hints, "纠偏：drive +search 不支持 --limit；需要更多结果时使用返回的 page_token 继续分页。类型过滤使用 --doc-types。")
	}
	if len(argv) >= 2 && argv[0] == "drive" && argv[1] == "+apply-permission" {
		if strings.Contains(lower, "invalid value \"public\"") || strings.Contains(lower, "pointless authorized request") || strings.Contains(lower, "permission-apply") {
			hints = append(hints, "纠偏：drive +apply-permission 只是向 owner 申请 view/edit 权限，不能设置“互联网所有人可见”。公开可阅读必须使用 drive permission.public patch：先 drive +inspect 确认 token/type，再 permission.public get 查看当前设置，最后 permission.public patch --params {\"token\":\"...\",\"type\":\"docx\"} --data {\"external_access\":true,\"link_share_entity\":\"anyone_readable\"} --yes，并再次 get 验证。")
		}
	}
	if len(argv) >= 3 && argv[0] == "drive" && argv[1] == "permission.public" && argv[2] == "patch" {
		switch {
		case strings.Contains(result, "91009"):
			hints = append(hints, "纠偏：91009 表示对外分享被租户安全策略管控，无法通过 API 或当前用户直接开启；应明确告知用户需要联系租户管理员调整组织级对外分享策略。")
		case strings.Contains(result, "91010"):
			hints = append(hints, "纠偏：91010 表示当前文档尚未打开对外分享；应提示用户先在文档权限设置中打开对外分享，再重试 permission.public patch。")
		case strings.Contains(result, "91011") || strings.Contains(result, "91012"):
			hints = append(hints, "纠偏：该错误表示对外分享或权限设置被文档密级策略拦截；应给出目标文档 URL，并提示用户在文档内发起密级豁免或降级后再重试。")
		}
	}
	if len(argv) >= 2 && argv[0] == "drive" && (argv[1] == "permission" || argv[1] == "file") && strings.Contains(strings.Join(argv, " "), "permission") && strings.Contains(lower, "available commands") {
		hints = append(hints, "纠偏：设置文档公开权限的真实命令组是 drive permission.public get/patch，不是 drive permission set 或 drive file permission set。")
	}
	if len(argv) >= 1 && argv[0] == "auth" && containsArg(argv, "--as") {
		hints = append(hints, "纠偏：auth/help/version/config 类命令不要添加 --as；查本人身份优先使用 auth list。")
	}
	if len(argv) >= 2 && argv[0] == "sheets" && strings.Contains(lower, "usage:") {
		hints = append(hints, "纠偏：电子表格任务先调用 lark_skill_view {\"name\":\"lark-sheets\"}；表格链接先用 lark_sheets_info 得到 sheet_id，再用 lark_sheets_read 读取明确范围，不要猜 Sheet1/0/1。")
	}
	if len(argv) >= 1 && (argv[0] == "docs" || argv[0] == "docx") && strings.Contains(lower, "--content is required") {
		hints = append(hints, "纠偏：创建文档必须带 content/markdown；不要只传 title。")
	}
	if len(hints) == 0 && (strings.Contains(lower, "usage:") || strings.Contains(lower, "available commands") || strings.Contains(lower, "unknown flag")) {
		hints = append(hints, "纠偏：不要原样重复失败命令；先调用对应 lark_skill_view 或 --help 确认真命令/参数后再重试。")
	}
	if len(hints) == 0 {
		return result
	}
	return strings.TrimSpace(result) + "\n\n" + strings.Join(hints, "\n") + "\n下一步要求：不要原样重复失败命令；按上面的纠偏先查 Skill/help/schema 或执行指定验证命令。"
}

func containsArg(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
		text := strings.TrimSpace(result.Text)
		if text == "" {
			text = "[no output]"
		}
		return ToolExecutionResult{Output: text, IsError: result.IsError}
	}
	if toolName == "mcp_resource_read" && runtime != nil {
		if !cfg.MCPEnabled {
			return ToolExecutionResult{Output: "[error] MCP 未启用", IsError: true}
		}
		uri := strings.TrimSpace(stringArg(args, "uri"))
		if uri == "" {
			return ToolExecutionResult{Output: "[error] uri 不能为空", IsError: true}
		}
		ctx, cancel := context.WithTimeout(parent, toolTimeout())
		defer cancel()
		text, err := runtime.ReadResource(ctx, uri)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if text == "" {
			text = "[empty resource]"
		}
		return ToolExecutionResult{Output: text}
	}
	if toolName == "mcp_prompt_get" && runtime != nil {
		if !cfg.MCPEnabled {
			return ToolExecutionResult{Output: "[error] MCP 未启用", IsError: true}
		}
		name := strings.TrimSpace(stringArg(args, "name"))
		if name == "" {
			return ToolExecutionResult{Output: "[error] name 不能为空", IsError: true}
		}
		var arguments map[string]any
		if raw, ok := args["arguments"].(map[string]any); ok {
			arguments = raw
		}
		ctx, cancel := context.WithTimeout(parent, toolTimeout())
		defer cancel()
		text, err := runtime.GetPrompt(ctx, name, arguments)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if text == "" {
			text = "[empty prompt]"
		}
		return ToolExecutionResult{Output: text}
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

func isDriveSearchSummary(result string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &payload); err != nil {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(payload["kind"])) == "drive_search"
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

// ==========================================
// 动态路径授权与安全文件读写自研系统
// ==========================================

type contextKey string

const (
	senderIDKey    contextKey = "sender_id"
	userMessageKey contextKey = "user_message"
)

func getUserMessage(ctx context.Context) string {
	if v := ctx.Value(userMessageKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type allowedPathsConfig struct {
	AllowedPaths []string `json:"allowed_paths"`
}

const (
	safeFileReadMaxBytes  = 1 * 1024 * 1024
	safeFileWriteMaxBytes = 1 * 1024 * 1024
	safeFileListMaxItems  = 200
)

type safeFileAccessLevel int

const (
	safeFileAccessNone safeFileAccessLevel = iota
	safeFileAccessRead
	safeFileAccessWrite
	safeFileAccessDelete
)

var (
	allowedPathsFilePathOverride  string
	errSafeFileAccessPathNotExist = fmt.Errorf("path does not exist")
)

func allowedPathsFilePath() (string, error) {
	if allowedPathsFilePathOverride != "" {
		return allowedPathsFilePathOverride, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "lingma-proxy", "allowed_paths.json"), nil
}

func loadAllowedPaths() []string {
	path, err := allowedPathsFilePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg allowedPathsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.AllowedPaths
}

func saveAllowedPaths(paths []string) error {
	path, err := allowedPathsFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	cfg := allowedPathsConfig{AllowedPaths: paths}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func safeFileConfiguredRoots(cfg Config) []SafeFilePathConfig {
	cfg = NormalizeConfig(cfg)
	if !cfg.SafeFiles.Enabled {
		return nil
	}
	roots := []SafeFilePathConfig{
		{Path: cfg.SafeFiles.WorkspaceDir, Mode: "delete"},
	}
	roots = append(roots, cfg.SafeFiles.ExtraPaths...)
	return roots
}

func absCleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	return filepath.Abs(filepath.Clean(path))
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := absCleanPath(path)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, errSafeFileAccessPathNotExist
		}
		return "", err
	}
	return filepath.Abs(filepath.Clean(realPath))
}

func canonicalAccessPath(path string) (string, error) {
	abs, err := absCleanPath(path)
	if err != nil {
		return "", err
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Abs(filepath.Clean(realPath))
	}
	parent := abs
	for {
		parent = filepath.Dir(parent)
		if parent == "." || parent == string(filepath.Separator) || parent == "" {
			break
		}
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			rel, relErr := filepath.Rel(parent, abs)
			if relErr != nil {
				return "", relErr
			}
			parts := append([]string{realParent}, strings.Split(rel, string(filepath.Separator))...)
			return filepath.Abs(filepath.Clean(filepath.Join(parts...)))
		}
	}
	return abs, nil
}

func isSubpath(root, target string) bool {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func safeFileAccessForPath(cfg Config, targetPath string) safeFileAccessLevel {
	cfg = NormalizeConfig(cfg)
	if !cfg.SafeFiles.Enabled {
		return safeFileAccessNone
	}
	target, err := canonicalAccessPath(targetPath)
	if err != nil {
		return safeFileAccessNone
	}
	level := safeFileAccessNone
	for _, rule := range safeFileConfiguredRoots(cfg) {
		root, rootErr := canonicalAccessPath(rule.Path)
		if rootErr != nil {
			continue
		}
		if !isSubpath(root, target) {
			continue
		}
		switch normalizeSafeFileMode(rule.Mode) {
		case "delete":
			if level < safeFileAccessDelete {
				level = safeFileAccessDelete
			}
		case "write":
			if level < safeFileAccessWrite {
				level = safeFileAccessWrite
			}
		default:
			if level < safeFileAccessRead {
				level = safeFileAccessRead
			}
		}
	}
	for _, allowed := range loadAllowedPaths() {
		root, rootErr := canonicalAccessPath(allowed)
		if rootErr == nil && isSubpath(root, target) && level < safeFileAccessRead {
			level = safeFileAccessRead
		}
	}
	return level
}

func hasSafeFileAccess(cfg Config, targetPath string, required safeFileAccessLevel) bool {
	return safeFileAccessForPath(cfg, targetPath) >= required
}

func deniedPathResult(path string) ToolExecutionResult {
	return ToolExecutionResult{
		Output:  fmt.Sprintf("[error] 拒绝访问：路径 %s 未获得足够权限。读取可让用户发送精确授权消息：授权目录 %s；写入或删除必须到 Feishu Agent 高级设置的“本机文件访问”中为该路径开启写入/删除权限。", path, filepath.Dir(filepath.Clean(path))),
		IsError: true,
	}
}

func userMessageHasExactPathCommand(userMsg string, commands []string, path string) bool {
	userMsg = strings.TrimSpace(strings.ReplaceAll(userMsg, "：", ":"))
	if userMsg == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	baseName := filepath.Base(cleanPath)
	for _, command := range commands {
		candidates := []string{
			command + " " + cleanPath,
			command + "：" + cleanPath,
			command + ": " + cleanPath,
			command + " " + baseName,
			command + "：" + baseName,
			command + ": " + baseName,
		}
		for _, candidate := range candidates {
			if strings.Contains(userMsg, candidate) {
				return true
			}
		}
	}
	return false
}

func isLikelyBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0
}

func executeSafeFileTool(parent context.Context, cfg Config, toolName string, args map[string]any) ToolExecutionResult {
	cfg = NormalizeConfig(cfg)
	if !cfg.SafeFiles.Enabled && toolName != "list_authorized_paths" {
		return ToolExecutionResult{Output: "[error] 本机文件工具已在 Feishu Agent 高级设置中关闭。", IsError: true}
	}
	if cfg.SafeFiles.Enabled && strings.TrimSpace(cfg.SafeFiles.WorkspaceDir) != "" {
		_ = os.MkdirAll(cfg.SafeFiles.WorkspaceDir, 0755)
	}
	switch toolName {
	case "safe_file_read":
		path := stringArg(args, "path")
		if !hasSafeFileAccess(cfg, path, safeFileAccessRead) {
			return deniedPathResult(path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if info.IsDir() {
			return ToolExecutionResult{Output: "[error] 目标是目录，请改用 safe_file_list。", IsError: true}
		}
		if info.Size() > safeFileReadMaxBytes {
			return ToolExecutionResult{Output: fmt.Sprintf("[error] 文件过大：%d bytes，当前安全读取上限为 %d bytes。", info.Size(), safeFileReadMaxBytes), IsError: true}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if isLikelyBinary(content) {
			return ToolExecutionResult{Output: "[error] 拒绝读取：目标看起来是二进制文件，safe_file_read 只支持文本文件。", IsError: true}
		}
		return ToolExecutionResult{Output: string(content)}

	case "safe_file_write":
		path := stringArg(args, "path")
		if !hasSafeFileAccess(cfg, path, safeFileAccessWrite) {
			return deniedPathResult(path)
		}
		cleanPath, err := absCleanPath(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		content := stringArg(args, "content")
		if len([]byte(content)) > safeFileWriteMaxBytes {
			return ToolExecutionResult{Output: fmt.Sprintf("[error] 写入内容过大：%d bytes，当前安全写入上限为 %d bytes。", len([]byte(content)), safeFileWriteMaxBytes), IsError: true}
		}
		parentDir := filepath.Dir(cleanPath)
		parentInfo, err := os.Stat(parentDir)
		if err != nil {
			return ToolExecutionResult{Output: "[error] 父目录不存在或不可访问: " + err.Error(), IsError: true}
		}
		if !parentInfo.IsDir() {
			return ToolExecutionResult{Output: "[error] 父路径不是目录。", IsError: true}
		}
		if !hasSafeFileAccess(cfg, parentDir, safeFileAccessWrite) {
			return deniedPathResult(parentDir)
		}
		overwrite := boolArg(args, "overwrite")
		if info, err := os.Stat(cleanPath); err == nil {
			if info.IsDir() {
				return ToolExecutionResult{Output: "[error] 目标是目录，不能写入文件内容。", IsError: true}
			}
			if !overwrite {
				return ToolExecutionResult{Output: fmt.Sprintf("[error] 目标文件已存在。覆盖需要用户发送“确认覆盖 %s”，然后再次调用 safe_file_write 并设置 overwrite=true。", filepath.Base(cleanPath)), IsError: true}
			}
			if !userMessageHasExactPathCommand(getUserMessage(parent), []string{"确认覆盖"}, cleanPath) {
				return ToolExecutionResult{Output: fmt.Sprintf("[error] 敏感操作拦截：覆盖文件需要用户最新消息精确包含“确认覆盖 %s”或“确认覆盖 %s”。", filepath.Base(cleanPath), cleanPath), IsError: true}
			}
			if err := os.WriteFile(cleanPath, []byte(content), 0644); err != nil {
				return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
			}
			return ToolExecutionResult{Output: "success"}
		} else if !os.IsNotExist(err) {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		file, err := os.OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		_, writeErr := file.WriteString(content)
		closeErr := file.Close()
		if writeErr != nil {
			return ToolExecutionResult{Output: "[error] " + writeErr.Error(), IsError: true}
		}
		if closeErr != nil {
			return ToolExecutionResult{Output: "[error] " + closeErr.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: "success"}

	case "safe_file_list":
		path := stringArg(args, "path")
		if !hasSafeFileAccess(cfg, path, safeFileAccessRead) {
			return deniedPathResult(path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if !info.IsDir() {
			return ToolExecutionResult{Output: "[error] 目标不是目录，请改用 safe_file_read。", IsError: true}
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("目录 %s 下的文件和子目录：\n", path))
		for i, entry := range entries {
			if i >= safeFileListMaxItems {
				sb.WriteString(fmt.Sprintf("- ... 已截断，仅显示前 %d 项\n", safeFileListMaxItems))
				break
			}
			info, err := entry.Info()
			size := int64(0)
			if err == nil {
				size = info.Size()
			}
			typeStr := "文件"
			if entry.IsDir() {
				typeStr = "目录"
			}
			sb.WriteString(fmt.Sprintf("- %s (%s, %d bytes)\n", entry.Name(), typeStr, size))
		}
		return ToolExecutionResult{Output: sb.String()}

	case "authorize_local_path":
		path := stringArg(args, "path")
		if strings.TrimSpace(path) == "" {
			return ToolExecutionResult{Output: "[error] 路径不能为空", IsError: true}
		}
		canonicalPath, err := canonicalExistingPath(path)
		if err != nil {
			if err == errSafeFileAccessPathNotExist {
				return ToolExecutionResult{Output: "[error] 授权路径必须是已存在的文件或目录。请先确认路径存在。", IsError: true}
			}
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		userMsg := getUserMessage(parent)
		if !userMessageHasExactPathCommand(userMsg, []string{"授权目录", "授权文件"}, canonicalPath) && !userMessageHasExactPathCommand(userMsg, []string{"授权目录", "授权文件"}, filepath.Clean(path)) {
			return ToolExecutionResult{
				Output:  fmt.Sprintf("[error] 授权被拒绝：只能在用户最新消息明确包含“授权目录 %s”或“授权文件 %s”时添加白名单。模型不能自行授权。", canonicalPath, canonicalPath),
				IsError: true,
			}
		}

		allowedPaths := loadAllowedPaths()
		alreadyExists := false
		for _, p := range allowedPaths {
			existing, err := canonicalAccessPath(p)
			if err == nil && existing == canonicalPath {
				alreadyExists = true
				break
			}
		}
		if !alreadyExists {
			allowedPaths = append(allowedPaths, canonicalPath)
			if err := saveAllowedPaths(allowedPaths); err != nil {
				return ToolExecutionResult{Output: "[error] 保存白名单失败: " + err.Error(), IsError: true}
			}
		}
		return ToolExecutionResult{Output: fmt.Sprintf("授权成功！路径 %s 已加入只读白名单。写入或删除仍需在 Feishu Agent 高级设置中显式开启。", canonicalPath)}

	case "list_authorized_paths":
		allowedPaths := loadAllowedPaths()
		var sb strings.Builder
		sb.WriteString("当前本地文件工具配置：\n")
		if !cfg.SafeFiles.Enabled {
			sb.WriteString("- 文件工具：已关闭\n")
			return ToolExecutionResult{Output: sb.String()}
		}
		sb.WriteString(fmt.Sprintf("- [workspace 可读写删] %s\n", filepath.Clean(cfg.SafeFiles.WorkspaceDir)))
		for _, rule := range cfg.SafeFiles.ExtraPaths {
			sb.WriteString(fmt.Sprintf("- [设置 %s] %s\n", normalizeSafeFileMode(rule.Mode), filepath.Clean(rule.Path)))
		}
		for _, p := range allowedPaths {
			sb.WriteString(fmt.Sprintf("- [口述授权只读] %s\n", p))
		}
		return ToolExecutionResult{Output: sb.String()}

	case "safe_file_delete":
		path := stringArg(args, "path")
		if !hasSafeFileAccess(cfg, path, safeFileAccessDelete) {
			return deniedPathResult(path)
		}
		cleanPath, err := absCleanPath(path)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}

		// 1. 优先校验目标是否为有效文件（禁止删除目录，防止灾难）
		info, err := os.Stat(cleanPath)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if info.IsDir() {
			return ToolExecutionResult{Output: "[error] 拒绝删除：目标是目录，只能删除文件。安全起见，禁止直接删除整个目录。", IsError: true}
		}

		// 2. 二次确认拦截与审计
		confirmed := boolArg(args, "confirmed")
		userMsg := getUserMessage(parent)
		filename := filepath.Base(cleanPath)
		hasExactConfirm := userMessageHasExactPathCommand(userMsg, []string{"确认删除"}, cleanPath)

		if !confirmed || !hasExactConfirm {
			return ToolExecutionResult{
				Output:  fmt.Sprintf("[error] 敏感操作拦截：删除文件 %s 需要用户进行二次验证。请在正文中向用户提出二次确认申请，提示用户发送 '确认删除 %s'。只有当用户发送了确认消息后，你才可以将 confirmed 设为 true 并再次执行删除。", filename, filename),
				IsError: true,
			}
		}

		if err := os.Remove(cleanPath); err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: "success"}

	default:
		return ToolExecutionResult{Output: "[error] 未知的文件安全工具", IsError: true}
	}
}
