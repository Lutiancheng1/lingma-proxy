# Lingma Feishu Agent — 技术架构与实现详解

> 本文档对应飞书云盘中的云端源文档（链接：https://www.feishu.cn/docx/FggndYCZaor2FyxF8hFcs1imnVc，Token: FggndYCZaor2FyxF8hFcs1imnVc）。修改时请确保与云端同步，勿直接在此处进行非同步性修改。

> 在飞书群里跟 AI 对话，它自己去读文档、查表格、搜日程、建任务、改权限，完了把结果流式打出来。你不需要切任何工具。

这不是一个聊天机器人。这是一个跑在飞书生态里的 AI Agent，有 30+ 工具、有记忆、会自己修错、能定时干活。

---

## 技术架构

```Plain Text
飞书用户发消息
      ↓
lark-cli 事件流（WebSocket/长轮询）
      ↓
Lingma Feishu Agent（本地桌面应用）
      ├── 事件处理 & 会话管理
      ├── 上下文预算管理（四级水位线）
      ├── 工具执行引擎（30+ 内置工具）
      ├── MCP 客户端（外部工具扩展）
      ├── Skills 引擎（lark-cli 技能系统）
      ├── 定时任务调度器
      ├── 工具记忆存储（SQLite + FTS5）
      └── 卡片输出引擎（CardKit 2.0 / Legacy / Markdown）
            ↓
      Lingma Proxy → LLM（Qwen/Kimi/MiniMax 等）
            ↓
      飞书 API（IM/日历/文档/表格/云盘/知识库）

```

---

## 一、跟普通飞书机器人有什么不一样

|  | 普通飞书机器人 | Lingma Feishu Agent |
|-|-|-|
| 对话 | 一问一答，没有上下文 | 多轮对话，自动管理上下文窗口，长聊不丢信息 |
| 工具 | 没有，或者只能查一个接口 | 30+ 结构化工具，覆盖飞书全生态 + MCP 外部扩展 |
| 输出 | 纯文本回复 | CardKit 流式卡片，70ms 打字机效果，工具面板折叠 |
| 记忆 | 无 | 工具记忆 FTS5 全文搜索 + 飞书历史消息检索 + 结构化压缩 |
| 出错 | 报错就停了 | 自主分析错误、查文档重试、权限不够自动发起登录 |
| 扩展 | 无 | MCP 协议 + Skills 系统，开放生态 |

---

## 二、30+ 工具，覆盖飞书全生态

Agent 内置的工具不是简单的 API 封装。每个工具都有结构化的参数定义（JSON Schema）、错误处理策略和结果分类策略。

### 飞书 IM

- `lark_im_send` 发消息到指定群聊
- `lark_im_search` 搜索聊天记录

### 日历

- `lark_calendar_agenda` 查看日程安排
- `lark_calendar_create` 创建日程事件

### 云文档

- `lark_docs_create` 创建飞书文档
- `lark_docs_fetch` 读取文档内容，长文档自动分块，支持 has_more/page_token 翻页

### 云盘

- `lark_drive_search` 搜索云盘文件，结果自动摘要（不塞一堆原始 JSON 给 LLM）
- `lark_permission_public` 管理文档公开权限（互联网所有人可见/只读）

### 电子表格

- `lark_sheets_info` 获取表格元信息（sheet 列表、行列数）
- `lark_sheets_read` 读取表格数据，大表格自动分块

### 多维表格

- `lark_base_records` 读取多维表格记录

### 任务

- `lark_task_list` 查看任务列表

### 知识库

- `lark_wiki_search` 搜索知识库内容

### 通用入口

- `lark_cli_exec` 通用 lark-cli 执行，覆盖所有飞书 API。不确定参数时，Agent 会先调 `lark_skill_view` 查阅对应 Skill 文档，不会瞎猜

### 本地文件操作

- `safe_file_read` / `safe_file_write` / `safe_file_list` / `safe_file_delete`
- 受权限白名单控制，默认只有 app 工作区可写，其他路径需要用户授权

### Web 工具

- `web_search` 网络搜索
- `web_fetch` 网页内容抓取，自动去 HTML 标签
- `weather_lookup` 天气查询

### 定时任务

- `schedule_task` 创建/管理定时任务，支持一次性延迟和 cron 循环

### AI 记忆

- `fetch_tool_memory` 搜索或获取历史工具结果（FTS5 全文搜索）
- `feishu_history_search` 搜索飞书群聊历史消息（超越当前上下文窗口）

### MCP 扩展

- `mcp_call` 调用外部 MCP 服务器工具
- `mcp_resource_read` 读取 MCP 资源
- `mcp_prompt_get` 获取 MCP 提示词模板

---

## 三、上下文管理，长聊不丢信息

这是 Lingma Feishu Agent 跟所有竞品拉开差距最大的地方。

普通 AI 助手的做法是：上下文满了就截断，截完前面聊的全忘了。我们的做法是分层管理，关键信息永远不会丢。

### 四级水位线

自动监控上下文窗口使用率，每级触发不同策略：

- **ok**（< 60%）：不干预
- **microcompact**（60–74%）：清理旧工具结果为 200 字符摘要，保留最近 3 个完整输出
- **compact**（75–84%）：触发 LLM 结构化压缩，生成 StructuredSummary（包含目标、决策、待办、实体、工具记忆引用）
- **critical**（85–91%）：强制压缩 + 最小化工具结果
- **blocking**（≥ 92%）：拒绝请求，提示用户手动 `/compact`

### 工具结果智能分类

不是所有工具结果都值得占同样的上下文空间。我们按工具类型分级处理：

- **preserve**（完整保留）：文档内容块、表格数据行、搜索结果 — 这些是用户真正要的东西
- **summarize**（结构化提取）：日程列表、任务列表、多维表格记录 — 提取关键字段（标题、时间、数量），不丢结构
- **stub**（仅元数据）：发消息、创建文档、权限操作 — 只保留成功/失败和关键 ID
- **discard**（占位符）：非关键辅助工具

这个分类是 `classifyToolResult` 函数自动完成的，按工具名快速分类，然后按内容启发式细化。

### 结构化压缩，不是盲截断

超 75% 水位线时，LLM 压缩器不是把前面的对话砍掉一半，而是生成一个结构化的 JSON 摘要：

```JSON
{
  "primary_goal": "用户的核心目标",
  "decisions": ["已做出的决策"],
  "actions": ["已完成的操作"],
  "pending_actions": ["待执行的操作"],
  "entities": ["关键实体（文档 token、表格 ID、任务链接）"],
  "tool_memory_refs": ["被压缩但可恢复的工具结果 ID"]
}

```

压缩 prompt 里有明确的加权指令：用户修正和偏好变更 > 最近工具结果 > 待执行操作。精确引用（文档 token、URL、记录 ID）必须保留原文，不能泛化成「之前那个文档」。

### 工具记忆，被压缩的也能找回来

所有工具结果持久化到 SQLite，建了 FTS5 全文索引。即使上下文压缩把某个工具的完整输出丢了，Agent 可以通过 `fetch_tool_memory` 工具把它检索回来。

- BM25 排序，搜索结果按相关性排
- 支持按 ID 精确获取（`action:get`）和关键词搜索（`action=search`）
- 工具结果占位符里带 memory ID，Agent 知道去哪里找

### 飞书历史搜索，超越上下文窗口

`feishu_history_search` 工具调用 `lark-cli im +messages-search`，搜索范围是整个群聊历史，不只是当前对话。

更聪明的是，Agent 会主动检测用户消息中的历史引用。当你说「上次那个文档」、「之前讨论的那个方案」，Agent 自动搜索飞书历史注入上下文，不需要你手动指定。

搜索结果缓存 10 分钟在 SQLite 里，避免重复调用。

---

## 四、流式卡片，不是纯文本

飞书群聊里的 AI 回复，绝大多数机器人给你一大段纯文本。我们用的是 CardKit 2.0 流式卡片，70ms 一个字打出来，跟 ChatGPT 的打字机效果一样。

### 三级降级

不是所有飞书环境都支持 CardKit。我们做了三级降级保证任何环境下都有输出：

1. **CardKit 2.0 流式卡片**：Element-level PUT + streaming_mode，70ms/字符打字机效果
2. **Legacy 1.0 PATCH**：整卡刷新 \~300ms
3. **纯 Markdown**：`lark-cli im +messages-reply`，最后兜底

### 卡片结构

```Plain Text
┌─────────────────────────────┐
│  Header（状态标题：正在思考/已完成）│
│  subtitle_md（会话信息）        │
├─────────────────────────────┤
│  steps_md（执行步骤）           │
│  [已完成] 查看日程              │
│  [已完成] 搜索文档              │
│  [进行中] 生成回复...           │
├─────────────────────────────┤
│  reply_md（AI 回复正文，流式）   │
│  这里是 AI 的流式回复内容...     │
├─────────────────────────────┤
│  ▶ 工具: lark_calendar_agenda │ ← 折叠面板
│  ▶ 工具: lark_drive_search    │ ← 折叠面板
├─────────────────────────────┤
│  hint_md（操作提示）            │
└─────────────────────────────┘

```

工具完成后自动插入 `collapsible_panel`，默认折叠，不干扰正文阅读。用户想看工具详情就展开，不想看就略过。

短回复完成后自动刷新 header 状态，不会停在「正在思考」。超长回复用紧凑最终卡片 + 分块 markdown 发送。

---

## 五、MCP 协议，无限扩展

Agent 内置完整的 MCP（Model Context Protocol 2025-06-18）客户端。这意味着你可以把任何工具通过 MCP 接入飞书 Agent。

三大能力域全支持：

- **Tools**：直接作为 LLM 工具调用
- **Resources**：通过 `mcp_resource_read` 伪工具暴露
- **Prompts**：通过 `mcp_prompt_get` 伪工具暴露

配置来源：`config.toml` 的 `[mcp_servers]`、JSON 配置文件、自动磁盘发现。启动时自动 `initialize` + `Sync()`，监听 `notifications/tools/list_changed` 通知自动重新同步。

---

## 六、定时任务，不用守着

在飞书群里直接创建定时任务，Agent 自己到点干活：

- **一次性延迟**：`schedule_task {"action":"create","delay_seconds":300,"prompt":"检查部署状态"}`
- **Cron 循环**：`schedule_task {"action":"create","cron":"0 9 * * 1-5","prompt":"生成每日站会报告"}`
- **静默模式**：prompt 里加 `[SILENT]`，执行完不发消息（适合后台监控）
- **多轮工具调用**：定时任务支持最多 8 轮工具调用，10 分钟超时
- **SQLite 持久化**：任务存数据库，应用重启自动恢复

管理命令：`/schedule` 列出所有任务，`/schedule pause <id>` 暂停，`/schedule resume <id>` 恢复，`/schedule run <id>` 手动触发。

---

## 七、安全，不是摆设

### 文件访问权限模型

- 默认只有 app 工作区可写
- 用户在飞书群发 `授权目录 /path` 或 `授权文件 /path` 添加授权
- 群聊授权只加只读白名单，不给写/删除权限
- 覆盖文件需要消息里包含 `确认覆盖 <文件名>`
- 删除文件需要 `confirmed: true` + `确认删除 <文件名>`
- 目录删除直接禁止
- 路径逃逸防护：解析真实路径，子路径匹配，防符号链接和前缀攻击

### 授权流程

Agent 不能自己执行 `auth login`。遇到权限不足时，Agent 自动发起设备流登录，返回授权链接给用户，用户授权后才继续。LLM 始终没有直接操作凭证的能力。

---

## 八、提示词工程，模块化

系统提示词不是一大坨文本，而是由 6 个规则文件按需组合：

- `base.md`：基础规则、工具记忆使用说明、飞书历史搜索、任务路由速查
- `feishu_cli_rules.md`：lark-cli 命令格式、常见陷阱（`+` 前缀、`--as bot`、schema 版本）
- `long_document_rules.md`：长文档读取策略（分块、has_more/page_token）
- `permission_rules.md`：权限管理规则
- `skill_rules.md`：技能使用规则
- `tool_error_recovery.md`：工具错误恢复协议

动态注入：Bot 身份描述、MCP 工具列表、技能索引。技能正文不默认注入（太长），通过 `lark_skill_view` 按需加载。

---

## 九、技能系统，自动发现

Agent 自动扫描本机已安装的 lark-cli Skills，在系统提示词中注入技能索引。LLM 不确定某个操作的参数时，先调 `lark_skill_view` 加载对应 Skill 文档，再执行命令。不会凭经验猜 `Sheet1`、`0`、`1` 这种参数。

支持 `SKILL.md` 元数据解析（description、when_to_use），LLM 可以根据用户意图自动选择最相关的 Skill。

---

## 十、错误恢复，自己修

工具执行失败时，Agent 不是简单报错然后停了。系统提示词里内置了完整的错误恢复协议：

1. **参数错误**：自动调用 `lark_skill_view` 查阅正确用法，然后重试
2. **权限错误**：Agent 自动发起设备流登录，返回授权链接
3. **Usage / unknown flag**：禁止原样重试，必须先查文档或 schema
4. **连续失败**：如果同一目标连续两次失败，必须先查 `lark_skill_view` 或 schema；仍不确定时向用户报告阻塞

---

## 十一、可观测性

### 桌面端日志系统

- 列表视图：时间、来源、级别、Session ID、Chat ID、消息摘要
- 详情弹窗：完整消息内容（不做截断）
- 搜索、筛选（按级别、来源）、区间选择、复制
- 反馈导出：脱敏 + 打包，包含日志、请求记录、配置摘要、环境信息

### 请求流

- 完整记录每个请求的 method、path、model、status code、duration、tokens
- 请求体和响应体完整保存（不做持久化截断）
- Token 统计按模型分组

---

## 十二、技术实现亮点

### 长对话不丢失上下文

四级水位线 + 工具结果智能分类 + 结构化压缩 + 工具记忆 FTS5 全文搜索 + 飞书历史搜索。五层机制叠加，确保长对话中关键信息不丢失。被压缩的工具结果可以通过 memory ID 按需恢复。

### 开箱即用

30+ 结构化工具覆盖飞书 IM、日历、云盘、文档、表格、任务、知识库。lark-cli 技能系统自动发现和按需加载。MCP 协议支持连接任意外部工具服务器。定时任务系统支持 cron 循环和一次性延迟。

### 企业级安全

文件访问白名单 + 确认覆盖/删除保护 + 路径逃逸防护 + 授权流程由 Agent 控制。LLM 不能直接执行 `auth login`，不能绕过权限白名单。

### 极致用户体验

CardKit 流式卡片 70ms 打字机效果 + 工具执行步骤实时展示 + 工具结果默认折叠 + 短回复自动刷新标题 + 三级降级保证任何环境都有输出。

### 自主错误恢复

工具错误不终止对话。Agent 自主分析错误类型、查阅文档重试、权限不够自动发起登录、参数不对自动查 Skill 文档。`Usage` / `unknown flag` 禁止盲目重试。

### 可观测性

完整的桌面端日志系统 + 请求流记录 + Token 统计 + 反馈导出。日志详情不做截断，请求/响应体完整保存。

---

## 十三、数据存储架构

| 数据类型 | 存储位置 | 说明 |
|-|-|-|
| 聊天记录 | 飞书云端 | 通过飞书 API 访问 |
| 文档/表格 | 飞书云端 | 通过飞书 API 访问 |
| 会话历史 | 本地 SQLite | 智能体运行数据，保证隐私和性能 |
| 工具记忆 | 本地 SQLite + FTS5 | 全文搜索索引，支持按需恢复 |
| 定时任务 | 本地 SQLite | 应用重启自动恢复 |
| 配置文件 | 本地文件系统 | `~/.config/lingma-proxy/` |

混合存储架构：核心业务数据在飞书云端（保证协作和同步），智能体运行数据在本地（保证隐私和性能）。
