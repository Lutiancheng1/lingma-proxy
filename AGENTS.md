# AGENTS.md

lingma-ipc-proxy — Lingma IDE Plugin API 适配层，提供 OpenAI/Anthropic 兼容接口。

**分支定位**：`feat/feishu-bridge-go` 为实验分支，仅通过 GitHub Actions 产出 artifact 供内测下载，**不参与 release 发布，不合入 main**。

## 项目定位

将 Lingma VS Code 插件的私有 IPC/HTTP 协议转换为标准的 OpenAI/Anthropic API，使第三方客户端（如 Claude Code、Continue、Cline 等）可以无缝接入 Lingma 后端模型（Qwen、Kimi、MiniMax 等）。

## 命令

```bash
cd /Users/tiancheng/OpenSources/lingma-ipc-proxy
gofmt -w .                          # 格式化
go build -o lingma-ipc-proxy ./cmd/lingma-ipc-proxy  # 编译
./lingma-ipc-proxy                  # 运行（前台）
nohup ./lingma-ipc-proxy > /tmp/lingma-proxy.log 2>&1 &  # 后台运行
pkill -f "lingma-ipc-proxy"         # 停止
./scripts/rebuild-local-app.sh      # 本地桌面版：打包 -> 制停旧进程 -> 覆盖 /Applications -> 重新打开
ENABLE_DEVTOOLS=0 ./scripts/rebuild-local-app.sh  # 本地桌面版：关闭 DevTools 右键菜单
```

## 强制规则（必须遵守）

### 1. 迭代文档维护（最高优先级）

**每次修改完代码后，必须立即更新 `ITERATION.md`。**

更新内容必须包括：
- 修改的文件和具体变更点
- 修改的原因/背景
- 遇到的问题和根因分析
- 解决状态（✅ 已解决 / ⚠️ 部分解决 / ❌ 未解决）
- 验证结果

**`ITERATION.md` 不提交到 Git，已加入 `.gitignore`。**

如果忘记更新，必须在下次对话开始时补全。

### 2. 代码修改规范

- 编译通过后再告知用户测试（`go build` 无错误）
- 修改后重启代理再测试（`pkill` + `nohup`）
- 本地桌面版覆盖安装必须使用 `./scripts/rebuild-local-app.sh`，禁止手工执行“退出/复制/打开”零散步骤
- Release workflow、Feishu artifact workflow 和默认本地重建都必须保留 Wails `-devtools` + `main.devtoolsBuild=true`，让桌面包右键菜单可打开 Inspect Element；只允许用 `ENABLE_DEVTOOLS=0` 关闭本地调试包
- 优先用 `search_replace` 修改现有文件，避免创建新文件
- 不主动创建 README/文档，除非用户明确要求
- 中文回复用户，代码注释保持英文

## 架构

- `cmd/lingma-ipc-proxy/` — 入口
- `internal/httpapi/` — HTTP API 层（OpenAI + Anthropic 路由）
- `internal/ipc/` — Lingma IPC 传输层（pipe/stdio）
- `internal/service/` — 业务逻辑（prompt 构建、session 管理、图片处理）
- `internal/toolemulation/` — Tool Emulation（prompt 注入 + action block 解析）
- `internal/feishu/` — Feishu Agent（飞书 Bot 收发 + CardKit 流式卡片 + lark-cli 集成 + MCP 客户端 + 上下文管理 + 系统提示词构建）

## Feishu Agent 踩坑警示

- **lark-cli `api` 子命令格式**：位置参数 `lark-cli api <method> <path> [flags]`，不是 `--method`/`--path`
- **lark-cli `+` 前缀命令**：skill 快捷命令用 `+` 前缀（`im +chat-list`、`im +messages-send`、`im +messages-search`、`im +messages-reply`、`calendar +agenda`、`calendar +create`、`docs +create`、`docs +fetch`），不带 `+` 的写法（`im chats list`、`im messages send`）无效。原生子命令不带 `+`（如 `drive file list`）
- **lark-cli `--format json`**：默认值就是 json，不需要显式传；`--json` 不是 `im +messages-reply` 的有效 flag
- **CardKit schema 版本**：CardKit API（`/cardkit/v1/cards`）需要 schema 2.0 + `element_id`；legacy `PATCH /im/v1/messages` 需要 schema 1.0，两者不通用
- **CardKit PUT body 格式**：`{"card": {"type":"card_json","data": <string>}, "sequence": N}`，`data` 是 JSON 字符串（双重编码），解码后传对象会报 `9499 Invalid parameter type`
- **CardKit element 必须初始创建**：`streamUpdateCardContent` 要求 element 在卡片创建时就存在，条件性跳过会导致后续更新 404
- **CardKit sequence**：同一卡片所有 API 调用的 `sequence` 必须严格递增，否则 300317 错误
- **CardKit 工具折叠面板**：工具完成后要通过结构刷新把该工具插入为默认折叠的 `collapsible_panel`；正文流继续写 `reply_md`，不能等最终卡片才展示工具面板
- **CardKit 完成态标题**：关闭 `/settings.streaming_mode` 不会刷新 header/title；短回复完成后仍需小尺寸最终卡片更新，否则标题会停在“正在思考”
- **CardKit 降级**：`cardWriter` 双模式（CardKit → legacy PATCH → markdown），`createAndSendStreamingCard` 失败自动 `useCardKit=false`

## 关键设计

- **图片传输**：通过本地缓存目录生成运行时专属 URI；QoderCN 使用 `qodercn:///agent/file?path=`，旧 Lingma 使用 `lingma:///agent/file?path=`
- **工具调用**：Prompt Injection 方式，模型输出 `{"tool":"NAME","parameters":{...}}` 格式 action block
- **Session 复用**：首次请求创建 session，后续请求复用以保持对话上下文
- **流式输出**：SSE 格式，Anthropic 使用 `content_block_start/delta/stop` 事件序列

## Feishu Agent 工具与 MCP

- **MCP 客户端**：支持 tools/resources/prompts 三大能力域，配置在 `config.toml` 的 `[mcp_servers]` 或 JSON。资源和提示词通过 `mcp_resource_read`/`mcp_prompt_get` 伪工具暴露给 LLM
- **工具结果分类**：`classifyToolResult` 按工具类型分为 preserve/summarize/stub/discard 四级，不再一刀切截断。修改分类逻辑在 `context_budget.go`
- **工具记忆**：所有工具结果存入 SQLite（FTS5 全文索引），LLM 可通过 `fetch_tool_memory` 工具搜索或按 ID 获取历史结果
- **飞书历史搜索**：`feishu_history_search` 工具调用 `lark-cli im +messages-search` 搜索当前群聊历史消息，带 10 分钟 SQLite 缓存
- **上下文压缩**：4 级水位线（ok/microcompact/compact/critical/blocking），超 75% 自动触发 LLM 结构化摘要。修改在 `manager.go` 的 `summarizeConversation`/`applyBudgetCompaction`
- **不要在工具结果处理中使用 `summarizeText` 做盲截断**：用 `classifyToolResult` + `extractToolResultSummary`/`extractToolResultStub` 替代

## 文档同步约定

本地 `docs/` 下的三份飞书智能体核心文档均与用户飞书云盘中的云端文档保持 1-to-1 对应关系，请勿脱离云端文档在本地直接编辑：

1. **Lingma Feishu Agent 食用指南**
   - 本地路径：`docs/feishu-agent-user-guide.md`
   - 云端链接：[https://www.feishu.cn/docx/BwacdC9evoNa1txuGUMcFVChnHd](https://www.feishu.cn/docx/BwacdC9evoNa1txuGUMcFVChnHd) (Token: `BwacdC9evoNa1txuGUMcFVChnHd`)
2. **基于 Lingma 账号资源的飞书个人专属智能体 (Pitch)**
   - 本地路径：`docs/feishu-agent-pitch.md`
   - 云端链接：[https://www.feishu.cn/docx/Mz3ldFZKvooIkdx6z4hcwn9Mnjb](https://www.feishu.cn/docx/Mz3ldFZKvooIkdx6z4hcwn9Mnjb) (Token: `Mz3ldFZKvooIkdx6z4hcwn9Mnjb`)
3. **Lingma Feishu Agent — 技术架构与实现详解**
   - 本地路径：`docs/feishu-agent-features.md`
   - 云端链接：[https://www.feishu.cn/docx/FggndYCZaor2FyxF8hFcs1imnVc](https://www.feishu.cn/docx/FggndYCZaor2FyxF8hFcs1imnVc) (Token: `FggndYCZaor2FyxF8hFcs1imnVc`)

修改这三份文档必须按下面流程执行，禁止跳步：

1. 修改本地文件前，先运行 `./scripts/sync-feishu-agent-docs.sh`。脚本必须使用 `lark-cli docs +fetch --api-version v2 --doc-format markdown --doc <token> --as user` 拉取云端 Markdown，不能改回 v1 `data.markdown` 或 `drive +export markdown` 路径，因为这些路径可能拿不到飞书图片 URL。
2. 同步后先检查图片数量。已有本地文件含图片时，如果云端拉取结果中的 `![](...)` 数量更少，必须停止，不能覆盖本地文件；这通常说明当前 CLI 导出没有暴露媒体块，而不代表飞书页面没有图片。
3. 本地修改时必须保留飞书导出的图片、附件、引用地址原样，尤其是 `internal-api-drive-stream.feishu.cn` 的图片 URL。除非用户明确要求换图，否则不要下载图片到本地再改相对路径。
4. 回写云端只能使用 v2 Markdown overwrite 形态：`lark-cli docs +update --api-version v2 --doc <token> --command overwrite --doc-format markdown --content @<relative-local-file> --as user`。`@file` 必须是相对路径。
5. 禁止使用 `lark-cli docs +update --api-version v1 --mode overwrite --markdown @file` 或 `--markdown @file` 这类旧参数整篇覆盖含图文档；它会触发 `IMAGE_DOWNLOAD_FAILED`，可能把云端图片块清掉。
6. 回写后必须重新执行 v2 Markdown fetch，只读验证：`warnings` 为空、图片数量未减少、旧版本直链/旧术语等目标问题已消失。本地与云端不要求图片临时 URL 字节级一致，因为飞书会重新生成 authcode URL，但图片数量和内容口径必须一致。
7. 完成后更新 `ITERATION.md`，记录云端 token、拉取/回写/验证命令、图片数量和结果，再提交 Git。

## 已知限制

- 工具调用依赖模型配合，**Qwen3-Coder 最可靠**，其他模型可能拒绝
- 图片传输依赖本地缓存目录结构
- Session 长时间不活动可能失效
