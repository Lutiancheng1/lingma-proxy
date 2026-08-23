# Remote 后端完善与使用指南

本文档记录对 **remote 后端**(直连 QoderCN / 灵码云端网关)的一整轮完善,以及完善后的使用方式。
目标:让 remote 模式成为一个**完整可用**的 OpenAI / Anthropic 兼容层,供 Claude Code、Codex、Cline、
cc-switch 等客户端直接接入。

所有行为均通过 mitmproxy 抓取 **QoderCN CLI**(`qoderclicn`,网关 `gateway.qoder.com.cn`)的真实流量、
对齐其协议后实现并实测。

---

## 1. 关键协议事实(逆向确认)

- **上游 = OpenAI Chat Completions 血统 + DeepSeek `reasoning_content` 扩展 + 阿里 `credits` 计费**。
  聊天 SSE 是**双层信封**:`data:{"body":"<转义的 chat.completion.chunk>","statusCodeValue":200}`。
- **触发"完整模型"(思考 + 计费)的是请求身份字段,不是 `Encode=1`**。`Encode=1` 只是 CLI 对请求体的
  一层可逆混淆(自定义字母表 base64),用不到。关键身份字段:`task_id="common"`、`business.type="agent"`、
  `model_config.source="system"` 等。旧代理用的 `task_id="question_refine"` / `jb_plugin/memory` 会被网关
  路由到**非推理、非计费**的降级路径。
- **网关接受但忽略** `max_tokens` / `stop`(实测 `max_tokens=200` 仍输出 1502 token;stop 不截断)。
  只有 `temperature` 生效 → 这两项由**代理侧强制**。
- **视觉**:图片以 **content-parts 内联 base64**(`{"type":"image_url","image_url":{"url":"data:...base64,..."}}`)
  即可,网关接受。实测**约 14 MB 以内**可用;≥16 MB 报 400、≥32 MB 报 413(需 OSS 上传,未实现)。
  CLI 用 OSS 上传只是它对大图的选择,非必需。
- **计费**:单位是 **credits**;每模型有 `price_factor`(0.1~0.6);账号级额度在
  `openapi.qoder.com.cn/api/v2/quota/usage`(个人池 + 组织池,单位 credits,仅需 Bearer access_token)。
  每请求的 `usage` 里带 `credits` / `original_credits` / `billable`。
- **凭据**:QoderCN CLI 登录后把凭据存在 `~/.qoder-cn/.auth/user`(AES-CBC 加密)+ `.auth/machine_id`。

---

## 2. 变更清单

对应分支 `feat/remote-backend-completion` 上的提交:

### 2.1 QoderCN CLI 凭据加载(`.auth/`)
`internal/remote/credentials.go`
- 除旧的 IDE 缓存布局(`cache/user` + `cache/id`)外,识别 CLI 布局:`.auth/user`(同样的 AES-CBC 加密)
  + `.auth/machine_id`。解密逻辑复用。
- 额外提取 `access_token`(用于 quota,见 2.4)。

### 2.2 完整 remote 适配
`internal/remote/client.go`、`internal/service/service.go`、`internal/httpapi/server.go`
- **解析上游已发但原先丢弃的字段**:`delta.reasoning_content`、`usage`(prompt/completion/total +
  `cached_tokens` + `reasoning_tokens`)、`finish_reason`。
- **真实 token**:用 usage 覆盖原来的 `estimateTokens` 估算;Anthropic usage 输出 `cache_read_input_tokens`
  (`input_tokens = prompt − cached`),OpenAI/Responses 输出 `*_tokens_details`。
- **请求身份对齐**:`task_id=common`、`business.product/type=cli/agent`、`model_config`(`source=system`、
  `format=openai`、`max_input_tokens`)、以及匹配的 `Cosy-*` 头 → 触发完整模型(思考流 + 计费)。
- **流式**:原生 `tool_calls` **实时增量**转发(OpenAI `delta.tool_calls`);`reasoning` 与 `content` 分流;
  OpenAI 流末尾补 `usage` 帧。
- **原生图片**:base64 走 content-parts,去掉 IPC 绕行与冗余的顶层 `image_urls`。
- **生成参数**:转发 `temperature/max_tokens/top_p/stop`;因网关忽略 `max_tokens`/`stop`,由 **`outputLimiter`
  代理侧强制**(截断可见输出 + 设置 `finish_reason`/`stop_reason`;`max_tokens` 为基于估算的近似上限)。
- 移除失效的 OSS 上传尝试与死代码。

### 2.3 非流式 completions 的 `reasoning_content`
`internal/httpapi/server.go`
- 非流式 `/v1/chat/completions` 的 `message` 也带 `reasoning_content`(此前只有流式带),让 cc-switch 等
  OpenAI→Anthropic 转换器在两条路上都能看到思考。

### 2.4 `GET /quota` 账号额度
`internal/remote/*`、`internal/httpapi/server.go`
- 新增 `GET /quota` 与 `/v1/quota`,返回账号 credits 额度。内部用 `access_token`(Bearer)查
  `openapi.qoder.com.cn/api/v2/quota/usage`,对外**不暴露原始 token**。

---

## 3. 暴露的端点

| 端点 | 说明 |
| --- | --- |
| `GET /v1/models`、`/api/v1/models`、`/api/tags` | 模型列表(供客户端 / cc-switch 自动探测) |
| `POST /v1/chat/completions`、`/api/v1/chat/completions` | OpenAI Chat Completions(**最完整/最忠实**) |
| `POST /v1/responses` | OpenAI Responses(基础可用,流式为缓冲式) |
| `POST /v1/messages` | Anthropic Messages(思考/工具/usage/图片都通;工具流为缓冲式) |
| `POST /v1/messages/count_tokens` | Anthropic token 计数(**估算值**) |
| `GET /quota`、`/v1/quota` | 账号 credits 额度(QoderCN) |
| `GET /health`、`/version`、`/props`、`/capabilities` | 健康/版本/能力 |

### 当前可用模型(示例账号)与计价因子
`price_factor` 为该模型的 credit 乘数(越低越便宜):

| 模型(key) | 展示名 | price_factor |
| --- | --- | --- |
| `qmodel` / `q37fmodel` / `dfmodel` | Qwen3.7-Plus / Flash、DeepSeek-V4-Flash | 0.1 |
| `mmodel` | MiniMax-M2.7 | 0.2 |
| `kmodel` | Kimi-K2.7-Code | 0.3 |
| `auto` / `qmodel_38max` / `qmodel_latest` / `dmodel` | Auto、Qwen3.x-Max、DeepSeek-V4-Pro | 0.5 |
| `gmodel` / `gm51model` | GLM-5.3 / 5.2 | 0.6 |

> 模型与可用性随账号 / 企业租户不同,以 `/v1/models` 实际返回为准。

---

## 4. 能力现状矩阵

| 能力 | OpenAI `/v1/chat/completions` | Anthropic `/v1/messages` |
| --- | --- | --- |
| 文本(流式/非流式) | ✅ | ✅ |
| **思考流** | ✅ `reasoning_content`(流+非流) | ✅ `thinking`/`thinking_delta` |
| 工具调用 | ✅ 原生,**流式真增量** | ✅ 结果正确,工具流为**缓冲式** |
| usage(真实 token + cached + reasoning) | ✅(流末尾也带) | ✅(`cache_read_input_tokens`) |
| 图片(≤~14MB) | ✅ 内联 base64 | ✅ 内联 base64 |
| `max_tokens` / `stop` | ✅ **代理侧强制**(近似) | ✅ **代理侧强制**(近似) |
| `finish_reason` / `stop_reason` | ✅ stop/length/tool_calls | ✅ end_turn/max_tokens/stop_sequence/tool_use |
| `count_tokens` | — | ⚠️ **估算** |
| `top_p`/`top_k`/`penalties`/`seed`/`logprobs`/`n`/`response_format` | ❌ 网关不支持(转发但不生效) | ❌ 同左 |
| 思考 `signature` | ❌(纯展示,不回传;不影响 cc-switch OpenAI 链路) | ❌ |

---

## 5. 使用方式

### 5.1 启动代理(remote 模式)
需要本机已用 QoderCN / 灵码 登录过(留下 `~/.qoder-cn/.auth/`)。

```bash
go build -o lingma-proxy ./cmd/lingma-ipc-proxy
./lingma-proxy --backend remote --port 8095
# 校验:
curl -s http://127.0.0.1:8095/v1/models | jq '.data[].id'
curl -s http://127.0.0.1:8095/quota
```
无需本地运行时常驻(remote 模式只读磁盘凭据 + 直连云端网关)。

### 5.2 直连(单进程,自包含)
- **OpenAI 兼容客户端**(Cline / Continue / Codex 的 chat 模式):
  base_url = `http://127.0.0.1:8095/v1`,模型填 `/v1/models` 里的 key(如 `qmodel`、`dmodel`)。
- **Claude Code**(Anthropic):`ANTHROPIC_BASE_URL=http://127.0.0.1:8095`,走 `/v1/messages`。
  开启 thinking 即可看到思考流。

### 5.3 经 cc-switch 管理(Anthropic 推荐走这条)
由于上游是 Chat Completions 血统,**把我们代理当成 `/v1/chat/completions`,Anthropic 方向交给 cc-switch**
转换,是更省心且工具流为真增量的方案:
```
Claude Code(Anthropic) → cc-switch(OpenAI⇄Anthropic) → 本代理(/v1/chat/completions) → QoderCN
```
- cc-switch 里新增一个 **OpenAI 兼容 provider**,base_url = `http://127.0.0.1:8095/v1`。
- 模型会被 cc-switch 通过 `/v1/models` **自动探测**。
- cc-switch 会把 `reasoning_content` 转成 Anthropic thinking,把 `tool_calls` 转成 `tool_use`,Claude Code 正常显示。
- Claude Code 侧对该模型**开启 thinking**,思考折叠区显示最顺。

### 5.4 让 cc-switch 显示额度
在 cc-switch 该 provider 的**用量脚本**中(选 CUSTOM 模板)填:
```js
({
  request: {
    url: "{{baseUrl}}/quota",   // baseUrl 带 /v1 也行(已注册 /v1/quota);或直接写 http://127.0.0.1:8095/quota
    method: "GET",
    headers: {}                 // 无需 token —— 代理内部持有并查询 openapi
  },
  extractor: function (r) {
    return {
      isValid:  !r.is_exceeded,
      planName: r.user_type,     // e.g. "teams"
      total:    r.total,         // 3000
      used:     r.used,
      remaining:r.remaining,
      unit:     r.unit           // "credits"
    };
  }
})
```
- cc-switch 的**代理层**还会自动解析响应里的 `usage`,显示每请求 token 数。
- 每请求"美元成本"不适用(QoderCN 按 credits 计,且每请求 credits 不在 wire 上,只在代理日志)。

### 5.5 直接查额度
```bash
curl -s http://127.0.0.1:8095/quota
# {"user_type":"teams","unit":"credits","total":3000,"used":0,"remaining":3000,
#  "percentage":0.01,"is_exceeded":false,"reset_at_ms":1790006400000, ...}
```

---

## 6. 限制与边界

- **`max_tokens`/`stop` 为代理侧近似强制**:无真 tokenizer,`max_tokens` cap 到 `maxTokens*3` runes 的估算;
  上游有时自己也截断(返回 `finish_reason=length`),两条路都正确映射。
- **超大图(>~14MB)**:内联 base64 会撞网关请求体上限;需 OSS 上传(其 `image/upload` 签名未破解,暂未实现)。
- **Anthropic 侧工具流为缓冲式**(结果正确但非实时增量);要实时增量走 cc-switch 链路即可。
- **`top_p`/`top_k`/`penalties`/`seed`/`logprobs`/`n`/`response_format`**:网关不支持,转发但不生效。
- **思考 `signature`**:不产出;对 cc-switch 的 OpenAI→Anthropic 链路无影响(思考纯展示、不回传)。
- **额度/凭据**:`/quota` 仅 QoderCN 账号可用;凭据需本机登录过 QoderCN/灵码。

---

## 7. 验证与测试

```bash
# 单元测试
go test ./internal/...

# 端到端(直连网关)
./lingma-proxy --backend remote --port 8095 &
# 思考 + 工具(OpenAI 流式:应看到 delta.reasoning_content、增量 tool_calls、末尾 usage 帧)
curl -N http://127.0.0.1:8095/v1/chat/completions -H 'content-type: application/json' \
  -d '{"model":"dmodel","stream":true,"messages":[{"role":"user","content":"用工具查巴黎天气"}],"tools":[...]}'
# Anthropic 思考(应有 thinking_delta + 真实 usage)
curl -N http://127.0.0.1:8095/v1/messages -H 'content-type: application/json' \
  -d '{"model":"dmodel","stream":true,"max_tokens":1024,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"一步步推理:17*23"}]}'
```

调试时可用 mitmproxy 观测发往 `gateway.qoder.com.cn` 的真实请求:
```bash
SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \
  ./lingma-proxy --backend remote --remote-proxy-url http://127.0.0.1:8080 --port 8095
```
