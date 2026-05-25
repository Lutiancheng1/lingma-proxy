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
- `internal/feishu/` — Feishu Bridge（飞书 Bot 收发 + CardKit 流式卡片 + lark-cli 集成 + 系统提示词构建）

## Feishu Bridge 踩坑警示

- **lark-cli `api` 子命令格式**：位置参数 `lark-cli api <method> <path> [flags]`，不是 `--method`/`--path`
- **lark-cli `+` 前缀命令**：skill 快捷命令用 `+` 前缀（`im +chat-list`、`im +messages-send`、`im +messages-search`、`im +messages-reply`、`calendar +agenda`、`calendar +create`、`docs +create`、`docs +fetch`），不带 `+` 的写法（`im chats list`、`im messages send`）无效。原生子命令不带 `+`（如 `drive file list`）
- **lark-cli `--format json`**：默认值就是 json，不需要显式传；`--json` 不是 `im +messages-reply` 的有效 flag
- **CardKit schema 版本**：CardKit API（`/cardkit/v1/cards`）需要 schema 2.0 + `element_id`；legacy `PATCH /im/v1/messages` 需要 schema 1.0，两者不通用
- **CardKit PUT body 格式**：`{"card": {"type":"card_json","data": <string>}, "sequence": N}`，`data` 是 JSON 字符串（双重编码），解码后传对象会报 `9499 Invalid parameter type`
- **CardKit element 必须初始创建**：`streamUpdateCardContent` 要求 element 在卡片创建时就存在，条件性跳过会导致后续更新 404
- **CardKit sequence**：同一卡片所有 API 调用的 `sequence` 必须严格递增，否则 300317 错误
- **CardKit 降级**：`cardWriter` 双模式（CardKit → legacy PATCH → markdown），`createAndSendStreamingCard` 失败自动 `useCardKit=false`

## 关键设计

- **图片传输**：通过本地缓存目录生成 `lingma:///agent/file?path=` URI
- **工具调用**：Prompt Injection 方式，模型输出 `{"tool":"NAME","parameters":{...}}` 格式 action block
- **Session 复用**：首次请求创建 session，后续请求复用以保持对话上下文
- **流式输出**：SSE 格式，Anthropic 使用 `content_block_start/delta/stop` 事件序列

## 已知限制

- 工具调用依赖模型配合，**Qwen3-Coder 最可靠**，其他模型可能拒绝
- 图片传输依赖本地缓存目录结构
- Session 长时间不活动可能失效
