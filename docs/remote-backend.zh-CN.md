# Remote 后端使用指南

**remote 后端**直连 QoderCN / 灵码云端网关(`gateway.qoder.com.cn`),把它包装成 **OpenAI / Anthropic 兼容**层,
供 Claude Code、Codex、Cline、cc-switch 等客户端直接接入。只读本机登录凭据 + 直连云端,**无需本地运行时常驻**。

前置:本机已用 QoderCN / 灵码登录过(存在 `~/.qoder-cn/.auth/`)。

---

## 端点

| 端点 | 说明 |
| --- | --- |
| `GET /v1/models`、`/api/v1/models`、`/api/tags` | 模型列表(客户端 / cc-switch 自动探测) |
| `POST /v1/chat/completions`、`/api/v1/chat/completions` | OpenAI Chat Completions(最完整) |
| `POST /v1/responses` | OpenAI Responses(流式为缓冲式) |
| `POST /v1/messages` | Anthropic Messages(思考/工具/usage/图片都通) |
| `POST /v1/messages/count_tokens` | Anthropic token 计数(**估算值**) |
| `GET /quota`、`/v1/quota` | 账号 credits 额度 |
| `GET /health`、`/version`、`/props`、`/capabilities` | 健康/版本/能力 |

模型以 `/v1/models` 实际返回为准(随账号/租户不同);每个模型有 `price_factor` 计价乘数(越低越便宜)。

---

## 能力矩阵

| 能力 | OpenAI `/v1/chat/completions` | Anthropic `/v1/messages` |
| --- | --- | --- |
| 文本(流式/非流式) | ✅ | ✅ |
| 思考流 | ✅ `reasoning_content`(流+非流) | ✅ `thinking` / `thinking_delta` |
| 工具调用 | ✅ 原生,流式真增量 | ✅ 原生,流式 `input_json_delta` |
| 思考强度 | ✅ `reasoning_effort` | ✅ `thinking`(effort / budget) |
| usage(真实 token + cached + reasoning) | ✅(流末尾也带) | ✅(`cache_read_input_tokens`) |
| 计费(credits / original_credits / billable) | ✅ 写入 `usage` | ✅ 写入 `usage` |
| 图片(≤~14MB) | ✅ 内联 base64 或 http(s) URL | ✅ 内联 base64 或 http(s) URL |
| `max_tokens` / `stop` | ✅ 代理侧强制(近似) | ✅ 代理侧强制(近似) |
| `finish_reason` / `stop_reason` | ✅ | ✅ |
| `count_tokens` | — | ⚠️ 估算 |
| `top_p`/`top_k`/`penalties`/`seed`/`logprobs`/`n`/`response_format` | ❌ 网关不支持 | ❌ 同左 |
| 本地文件路径图片(`file://` / 绝对路径) | ❌ 一律拒绝 | ❌ 一律拒绝 |

---

## 使用

### 启动

```bash
go build -o lingma-proxy ./cmd/lingma-ipc-proxy
./lingma-proxy --backend remote --port 8095
# 校验
curl -s http://127.0.0.1:8095/v1/models | jq '.data[].id'
curl -s http://127.0.0.1:8095/quota
```

### 直连(单进程,自包含)

- **OpenAI 兼容客户端**(Cline / Continue / Codex chat):base_url = `http://127.0.0.1:8095/v1`,
  模型填 `/v1/models` 里的 key(如 `qmodel`、`dmodel`)。
- **Claude Code**(Anthropic):`ANTHROPIC_BASE_URL=http://127.0.0.1:8095`,走 `/v1/messages`;开启 thinking 可见思考流。

### 经 cc-switch 管理(Anthropic 可选此路)

上游是 Chat Completions 血统,也可以把本代理当成 OpenAI provider,Anthropic 方向交给 cc-switch 转换:

```
Claude Code(Anthropic) → cc-switch(OpenAI⇄Anthropic) → 本代理(/v1/chat/completions) → QoderCN
```

- cc-switch 里新增一个 **OpenAI 兼容 provider**,base_url = `http://127.0.0.1:8095/v1`;模型由 `/v1/models` 自动探测。
- cc-switch 把 `reasoning_content` 转 thinking、`tool_calls` 转 `tool_use`,Claude Code 正常显示。

### 在 cc-switch 里显示额度

用仓库自带脚本 `scripts/cc-switch-usage.js`:cc-switch → 该 provider → 用量查询,模板选 **CUSTOM**,
base URL 填代理地址、API key 填代理入站 key,粘贴该脚本。它查 `/quota` 并映射出 total/used/remaining/unit/planName。

### 直接查额度

```bash
curl -s -H "x-api-key: <代理入站 key>" http://127.0.0.1:8095/quota
# {"user_type":"teams","unit":"credits","total":3000,"used":63,"remaining":2937,
#  "percentage":0.03,"is_exceeded":false,"reset_at_ms":1790006400000, ...}
```

### 入站鉴权(可选,对外暴露时开启)

默认不鉴权,仅靠绑定 `127.0.0.1`。要经 Cloudflare Tunnel 等对外暴露时,加
`--auth-keys-file <文件>`(一行一个 key,`#` 注释)。客户端用 `x-api-key` 或 `Authorization: Bearer` 携带;
无效/缺失 key 直接断连、不返回任何内容。开启后所有端点(含 `/quota`)都需 key。

---

## 限制与边界

- **`max_tokens` / `stop` 为代理侧近似强制**:网关忽略这两项,代理按估算 token(`maxTokens*3` runes)截断可见输出并设置
  `finish_reason` / `stop_reason`;上游有时也会自截断(`finish_reason=length`),两条路都正确映射。
- **`count_tokens` 为估算**:文本 rune/3 + 每图约 1600,网关无分词器端点。
- **超大图(>~14MB)**:内联 base64 会撞网关请求体上限(≥16MB 报 400、≥32MB 报 413)。可改用 http(s) 图片 URL(由网关抓取)。
- **本地文件路径图片一律拒绝**:`image_url` 只接受 `data:` 内联与 `http(s)` URL;`file://` / `~` / 绝对路径不读(防本机文件外泄)。
- **`top_p`/`top_k`/`penalties`/`seed`/`logprobs`/`n`/`response_format`**:网关不支持,转发但不生效。
- **思考 `signature`**:不产出(思考纯展示,不回传);对 cc-switch 的 OpenAI→Anthropic 链路无影响。
- **额度 / 凭据**:`/quota` 仅 QoderCN 账号可用;凭据需本机登录过 QoderCN / 灵码。

---

## 自测

```bash
go test ./internal/...

./lingma-proxy --backend remote --port 8095 &
# OpenAI 流式:应看到 delta.reasoning_content、增量 tool_calls、末尾 usage 帧
curl -N http://127.0.0.1:8095/v1/chat/completions -H 'content-type: application/json' \
  -d '{"model":"dmodel","stream":true,"messages":[{"role":"user","content":"用工具查巴黎天气"}],"tools":[...]}'
# Anthropic 思考:应有 thinking_delta + 真实 usage
curl -N http://127.0.0.1:8095/v1/messages -H 'content-type: application/json' \
  -d '{"model":"dmodel","stream":true,"max_tokens":1024,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"一步步推理:17*23"}]}'
```
