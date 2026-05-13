# AGENTS.md

lingma-ipc-proxy — Lingma IDE Plugin API 适配层，提供 OpenAI/Anthropic 兼容接口。

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
- 优先用 `search_replace` 修改现有文件，避免创建新文件
- 不主动创建 README/文档，除非用户明确要求
- 中文回复用户，代码注释保持英文

## 架构

- `cmd/lingma-ipc-proxy/` — 入口
- `internal/httpapi/` — HTTP API 层（OpenAI + Anthropic 路由）
- `internal/ipc/` — Lingma IPC 传输层（pipe/stdio）
- `internal/service/` — 业务逻辑（prompt 构建、session 管理、图片处理）
- `internal/toolemulation/` — Tool Emulation（prompt 注入 + action block 解析）

## 关键设计

- **图片传输**：通过本地缓存目录生成 `lingma:///agent/file?path=` URI
- **工具调用**：Prompt Injection 方式，模型输出 `{"tool":"NAME","parameters":{...}}` 格式 action block
- **Session 复用**：首次请求创建 session，后续请求复用以保持对话上下文
- **流式输出**：SSE 格式，Anthropic 使用 `content_block_start/delta/stop` 事件序列

## 已知限制

- 工具调用依赖模型配合，**Qwen3-Coder 最可靠**，其他模型可能拒绝
- 图片传输依赖本地缓存目录结构
- Session 长时间不活动可能失效
