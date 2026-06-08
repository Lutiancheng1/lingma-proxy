## QoderWork CN 集成到 lingma-ipc-proxy — 最终实现方案

### 一、核心结论

**路径 A（子进程 spawn qodercli）是唯一可行的方案。** 路径 B（直连 Gateway API）**不可行**。

路径 A 经验证有两种子模式：

- **A1 轻量模式**（`-p` 单次调用）：每次请求 spawn 一个 qodercli 进程，无需维护长驻进程，适合简单单轮对话
- **A2 交互模式**（stream-json 双向通信）：长驻 qodercli 进程，stdin/stdout 双向 NDJSON，支持多轮对话和完整工具调用

两种模式均已在本机实测通过。

---

### 二、路径 B 不可行的完整论证

#### 2.1 签名机制分析

QoderWork CN 的网关请求使用 `Bearer COSY.<base64_payload>.<md5_signature>` 认证，所需 HTTP 头包括：

| Header | 来源 |
|--------|------|
| `Authorization: Bearer COSY.%s.%s` | qodercli 内部签名函数生成 |
| `Cosy-Key` | UMID C 库基于硬件指纹动态生成 |
| `Cosy-MachineToken` | UMID SecurityGuard `sgGetSecurityToken()` |
| `Cosy-User` | `.auth-cn/user` 中的 `uid` |
| `Cosy-Date` | 当前时间戳 |
| `Cosy-ClientType` | 客户端类型标识 |
| `X-Machine-Id` | `~/.qoderworkcn/machine-id` |
| `X-Machine-Type` | UMID 硬件指纹类型码 |

#### 2.2 `qodercli internal sign-request` 命令

二进制中发现了一个内部签名命令：

```bash
QODERCLI_INTERNAL_ACCESS=true qodercli internal sign-request --url "/api/v2/service/pro/imageSearch"
```

**限制：**
- 有 URL 白名单机制，聊天接口 `/api/v2/service/pro/sse/agent_chat_generation` 不在白名单中 → 返回 `"url_not_allowed"`
- 白名单内的 URL（如 `/api/v2/service/pro/imageSearch`）需要 CLI 登录状态 → 返回 `"not_logged_in"`
- 即使能签名白名单 URL，也无法签名聊天接口

#### 2.3 UMID SecurityGuard 不可复制

签名密钥由编译在 qodercli 内的 UMID 原生 C 库（SecurityGuard）生成：

```
umid.InitUMIDModule()
  → cgoInitUMID()
    → sgInitUMID()              # C 函数，采集硬件指纹
    → sgGetSecurityToken()      # 返回 88 字符 machineToken
    → sgFreeSecurityToken()     # 释放
```

- 通过 CGO 静态链接编译进 qodercli（~40MB 二进制），无独立 `.dylib`
- 签名密钥基于硬件指纹动态生成，不存储在文件中
- 没有 C 头文件或动态库可供外部调用
- `qodercli machine-info` 可以**读取** machineToken，但这是只读访问，不能用于签名

#### 2.4 请求体加密（`Encode=1`）

聊天接口 URL 为 `/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1`，`Encode=1` 表示请求体需要自定义加密：

```
JSON → AES 加密 → shuffle() 字节打乱 → 自定义 Base64 编码 → 附加 XXHash 校验
```

自定义 Base64 使用非标准编码表 + `shuffle()` 置换，无法从外部复制。

#### 2.5 尝试总结

| 尝试 | 结果 |
|------|------|
| 用 `access_token` 作为 CosyKey 签名 | ❌ "Signature invalid" |
| 用 `security_oauth_token` 签名 | ❌ 同上 |
| 用各种 info payload 变体 | ❌ 全部 "Signature invalid" |
| 用 `qodercli internal sign-request` | ❌ "url_not_allowed"（聊天接口不在白名单） |
| 从 `machine-info` 获取 token 构造签名 | ❌ 仅能读取 token，无法复现签名流程 |
| **根本原因** | 签名密钥由 UMID C 库硬件绑定生成 + Encode=1 请求体加密 |

**结论：** 直连 QoderWork CN 网关需要同时逆向 UMID SecurityGuard 签名算法和 Encode=1 加密算法，工程上不可行。

---

### 三、路径 A：子进程方式（完整实现方案）

#### 3.1 运行环境

Electron 应用通过 `buildSDKEnv()` 函数设置以下环境变量来启动 qodercli：

```bash
# 必需的环境变量
export QODERCLI_INTEGRATION_MODE=qoder_work    # 告知 CLI 处于集成模式
export QODER_API_ENCRYPTION_BYPASS=true        # 跳过 API 加密（关键！）
export QODER_SITE=cn                           # 中国站
export QODER_PERSONAL_ACCESS_TOKEN=""           # 置空，使用 .auth-cn/user 凭据

# 必需参数
--storage-dir ~/.qoderworkcn                   # 指向认证凭据目录
--site cn                                      # 中国站
```

**关键发现：** `QODER_API_ENCRYPTION_BYPASS=true` 这个环境变量绕过了 Encode=1 请求体加密，使 qodercli 内部直接使用明文 JSON。这也是我们能成功调用 qodercli 的前提。

#### 3.2 子模式 A1：轻量 `-p` 模式

**适合场景：** 单轮对话、简单的 prompt → response 调用

```bash
QODERCLI_INTEGRATION_MODE=qoder_work \
QODER_API_ENCRYPTION_BYPASS=true \
QODER_SITE=cn \
QODER_PERSONAL_ACCESS_TOKEN="" \
"/Applications/QoderWork CN.app/Contents/Resources/bin/qodercli" \
  --storage-dir ~/.qoderworkcn \
  --site cn \
  --model q36fmodel \
  -p "Hello, how are you?" \
  --max-turns 1 \
  -q \
  -f stream-json
```

**输出格式（NDJSON，3 行）：**

```json
{"type":"system","subtype":"init","model":"Qwen3.6-Flash","tools":[...],"session_id":"...","uuid":"..."}
{"type":"assistant","message":{"role":"assistant","content":[{"text":"Hello! How can I help?","type":"text"}]},"session_id":"...","uuid":"..."}
{"type":"result","subtype":"success","result":"Hello! How can I help?","duration_api_ms":1841,"num_turns":1,"usage":{...},"uuid":"..."}
```

**A1 优势：**
- 实现极其简单，`exec.Command()` + 解析 3 行 NDJSON
- 无长驻进程，无状态管理
- 每次请求独立，天然无并发问题

**A1 劣势：**
- 每次请求有进程启动开销（~200ms）
- 不支持多轮对话（除非通过 `--resume` 传入 session ID）
- 不支持流式输出（`-f stream-json` 会等所有输出完成后才一次性返回）

#### 3.3 子模式 A2：交互 stream-json 模式

**适合场景：** 多轮对话、流式输出、工具调用

**启动命令：**

```bash
QODERCLI_INTEGRATION_MODE=qoder_work \
QODER_API_ENCRYPTION_BYPASS=true \
QODER_SITE=cn \
QODER_PERSONAL_ACCESS_TOKEN="" \
"/Applications/QoderWork CN.app/Contents/Resources/bin/qodercli" \
  --storage-dir ~/.qoderworkcn \
  --site cn \
  --model qmodel_latest \
  --input-format stream-json \
  --output-format stream-json \
  --yolo
```

**输入消息（stdin → qodercli）：**

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Hello"}]}}
{"type":"control_request","request":{"subtype":"set_model","model":"kmodel"}}
{"type":"control_response","response":{"behavior":"allow"},"request_id":"..."}
```

**输出消息（qodercli → stdout）：**

| type | subtype | 关键字段 | 用途 |
|------|---------|---------|------|
| `system` | `init` | `model`, `tools[]`, `skills[]`, `session_id` | 会话初始化（进程启动后第一条） |
| `stream_event` | — | `event.type`: content_block_delta | 流式增量输出 |
| `assistant` | — | `message.content[]`: text/thinking/tool_use | 完整助手回复 |
| `user` | — | `message.content[]`: tool_result | 工具执行结果 |
| `result` | `success` | `result`, `duration_api_ms`, `usage` | 会话结束标记 |
| `control_request` | `can_use_tool` | `request_id`, `request.input` | 权限确认请求（`--yolo` 模式下不出现） |
| `prompt_suggestion` | — | `suggestion` | 后续提示建议 |

**`stream_event.event.delta` 类型：**

| delta.type | 数据字段 | 映射到 StreamEvent |
|-----------|---------|-------------------|
| `thinking_delta` | `delta.thinking` | `{Type: "thinking", Delta: ...}` |
| `text_delta` | `delta.text` | `{Type: "text", Delta: ...}` |
| `input_json_delta` | `delta.partial_json` | `{Type: "tool_call", Delta: ...}` |

**A2 优势：**
- 真正的流式输出，逐 token 推送
- 支持多轮对话（同一进程内连续发送消息）
- 支持工具调用（qodercli 原生 tool_use 协议）
- 进程复用，无重复启动开销

**A2 劣势：**
- 需要管理长驻进程的生命周期
- 单进程单会话，并发需进程池
- 进程崩溃需要重启和恢复

#### 3.4 可用模型

| Key | 显示名 | 价格因子 |
|-----|--------|---------|
| `auto` | Auto（自动选择） | 0.5 |
| `qmodel_latest` | Qwen3.7-Max | 0.2 |
| `gm51model` | GLM-5.1 | 0.6 |
| `kmodel` | Kimi-K2.6 | 0.3 |
| `qmodel` | Qwen3.7-Plus | 0.1 |
| `q36fmodel` | Qwen3.6-Flash | 0.1 |
| `dmodel` | DeepSeek-V4-Pro | 0.5 |
| `dfmodel` | DeepSeek-V4-Flash | 0.1 |

模型列表来源：`qodercli --help` 的 `--model` flag 描述，以及 `system.init` 消息中的 `model` 字段。

#### 3.5 认证体系

| 文件 | 格式 | 内容 |
|------|------|------|
| `~/.qoderworkcn/.auth-cn/id` | 纯文本 UUID | 设备标识（也用于 AES 密钥源） |
| `~/.qoderworkcn/.auth-cn/user` | Base64(AES-128-CBC) | `access_token`（`dt-s` 前缀，27 字符）、`uid`、`refresh_token`、`user_type`、`user_tag`、`expire_time` |
| `~/.qoderworkcn/machine-id` | 纯文本 hex | 16 字符设备指纹种子 |

解密 `.auth-cn/user`：AES-128-CBC，密钥 = `.auth-cn/id`[:16]，IV = 密钥。

**注意：** QoderWork CN 的 `.auth-cn/user` 中**没有** `key`（CosyKey）和 `encrypt_user_info` 字段——这与 Lingma/QoderCN IDE 插件的 `cache/user` 格式完全不同。两者是独立的产品，凭据不互通。

---

### 四、代码结构

#### 4.1 新增包 `internal/qodercli/`

**`message.go`（~150 行）：** NDJSON 消息类型定义

```go
package qodercli

type OutputMessage struct {
    Type      string `json:"type"`      // system, assistant, user, result, stream_event, control_request
    SubType   string `json:"subtype,omitempty"`
    UUID      string `json:"uuid,omitempty"`
    SessionID string `json:"session_id,omitempty"`
    Model     string `json:"model,omitempty"`
    Tools     []string `json:"tools,omitempty"`
    Message   *AgentMessage `json:"message,omitempty"`
    Result    string `json:"result,omitempty"`
    DurationAPIMs int64 `json:"duration_api_ms,omitempty"`
    Usage     *TokenUsage `json:"usage,omitempty"`
    Event     *StreamEvent `json:"event,omitempty"`
    IsError   bool `json:"is_error,omitempty"`
    NumTurns  int `json:"num_turns,omitempty"`
}

type AgentMessage struct {
    Role    string         `json:"role"`
    Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
    Type     string          `json:"type"`     // text, thinking, tool_use, tool_result
    Text     string          `json:"text,omitempty"`
    Thinking string          `json:"thinking,omitempty"`
    ID       string          `json:"id,omitempty"`
    Name     string          `json:"name,omitempty"`
    Input    json.RawMessage `json:"input,omitempty"`
}

type StreamEvent struct {
    Type         string       `json:"type"`
    Index        int          `json:"index,omitempty"`
    ContentBlock *ContentBlock `json:"content_block,omitempty"`
    Delta        *EventDelta  `json:"delta,omitempty"`
}

type EventDelta struct {
    Type        string `json:"type"`        // thinking_delta, text_delta, input_json_delta
    Thinking    string `json:"thinking,omitempty"`
    Text        string `json:"text,omitempty"`
    PartialJSON string `json:"partial_json,omitempty"`
    StopReason  string `json:"stop_reason,omitempty"`
}

type InputMessage struct {
    Type      string        `json:"type"`
    SessionID string        `json:"session_id,omitempty"`
    Message   *AgentMessage `json:"message,omitempty"`
    Request   *ControlReq   `json:"request,omitempty"`
    Response  *ControlResp  `json:"response,omitempty"`
    RequestID string        `json:"request_id,omitempty"`
}
```

**`client.go`（~350 行）：** 进程管理 + 消息收发

```go
type Config struct {
    BinaryPath     string   // qodercli 路径
    StorageDir     string   // ~/.qoderworkcn
    Model          string   // 默认模型
    Site           string   // "cn"
    PermissionMode string   // "yolo"
    Mode           string   // "lightweight" (A1) 或 "interactive" (A2)
}

type Client struct {
    cfg     Config
    cmd     *exec.Cmd
    stdin   io.WriteCloser
    outCh   chan OutputMessage
    running atomic.Bool
}

// Start    → spawn qodercli 子进程，设置环境变量，启动 readLoop
// Send     → 向 stdin 写入 NDJSON（A2 模式）
// Stop     → 关闭 stdin + kill 进程
// Chat     → 发送 user 消息，等待 result，返回 ChatResponse
// ChatStream → 发送 user 消息，返回 StreamChunk channel
```

**环境变量设置（`buildEnv()` 方法）：**

```go
func (c *Client) buildEnv() []string {
    env := os.Environ()
    env = append(env,
        "QODERCLI_INTEGRATION_MODE=qoder_work",
        "QODER_API_ENCRYPTION_BYPASS=true",
        "QODER_SITE=cn",
        "QODER_PERSONAL_ACCESS_TOKEN=",
    )
    return env
}
```

#### 4.2 修改 `internal/service/service.go`

```go
const BackendQoderCLI BackendMode = "qodercli"

// Config 新增
QoderCLIBinary  string
QoderCLIStorage string

// Service 新增
qoderClient *qodercli.Client

// Generate() 新增路由
case BackendQoderCLI:
    return s.generateQoderCLI(ctx, req)

// GenerateStream() 新增路由
case BackendQoderCLI:
    return s.generateQoderCLIStream(ctx, req)

// ListModels() 新增
case BackendQoderCLI:
    return s.qoderCLIModels(), nil
```

#### 4.3 修改 `cmd/lingma-ipc-proxy/main.go`

```go
flags.StringVar(&cfg.QoderCLIBinary, "qodercli-binary", "", "qodercli binary path")
flags.StringVar(&cfg.QoderCLIStorage, "qodercli-storage-dir", "", "qodercli storage dir")
```

#### 4.4 配置文件 `config.example.json`

```json
{
  "host": "127.0.0.1",
  "port": 8095,
  "backend": "qodercli",
  "model": "qmodel_latest",
  "qodercli_binary": "/Applications/QoderWork CN.app/Contents/Resources/bin/qodercli",
  "qodercli_storage_dir": "~/.qoderworkcn",
  "qodercli_mode": "interactive",
  "session_mode": "reuse",
  "timeout": 300,
  "available_models": [
    "auto", "qmodel_latest", "gm51model", "kmodel",
    "qmodel", "q36fmodel", "dmodel", "dfmodel"
  ]
}
```

---

### 五、关键设计决策

**A1 vs A2 选择：** 推荐 A2（交互模式）作为默认。A1 适合作为快速验证或简单场景的备选。

**进程生命周期（A2）：** 长驻模式。首次请求启动 qodercli，保持 stdin 打开，后续复用。崩溃自动重启。

**会话管理：** `--session-id` 控制。reuse 模式用固定 ID（多轮对话），fresh 模式每次新 ID（单轮）。

**工具调用：** 复用现有 `toolemulation` 层。将工具定义注入 system prompt（`InjectTooling()`），qodercli 模型生成 action block，`ParseActionBlocks()` 解析。`--yolo` 模式避免 control_request 交互。

**流式输出：** A2 模式通过 `stream_event` 实现真正的逐 token 流式。将 `text_delta` / `thinking_delta` 映射到 `StreamEvent`。

**并发：** 每个 qodercli 进程同时只能处理一个请求。需并发时启动进程池（可选优化）。

**依赖：** 需要用户安装 QoderWork CN 并在其中登录。qodercli 二进制路径和 `.auth-cn/` 凭据由 QoderWork CN 管理。

---

### 六、实施步骤

1. 创建 `internal/qodercli/message.go`：消息类型定义（~150 行）
2. 创建 `internal/qodercli/client.go`：进程管理 + 消息收发（~350 行）
3. 修改 `internal/service/service.go`：新增 `BackendQoderCLI` 路由（~80 行改动）
4. 修改 `cmd/lingma-ipc-proxy/main.go`：新增配置项（~20 行改动）
5. 更新 `config.example.json`：新增 qodercli 示例
6. 端到端测试：`curl http://localhost:8095/v1/chat/completions -d '{"model":"qmodel_latest","messages":[{"role":"user","content":"hello"}]}'`

预计总代码量：~600 行新增 + ~100 行修改。

---

### 附录 A：完整验证记录

#### A1 轻量模式验证

```bash
# 测试命令
QODERCLI_INTEGRATION_MODE=qoder_work QODER_API_ENCRYPTION_BYPASS=true \
QODER_SITE=cn QODER_PERSONAL_ACCESS_TOKEN="" \
"/Applications/QoderWork CN.app/Contents/Resources/bin/qodercli" \
  --storage-dir ~/.qoderworkcn --site cn --model q36fmodel \
  -p "Reply with exactly: MODEL_TEST_OK" --max-turns 1 -q -f stream-json

# 结果：成功返回 MODEL_TEST_OK，模型 Qwen3.6-Flash，耗时 1841ms
```

#### A2 交互模式验证

```bash
# 启动 qodercli，通过 stdin 发送消息
echo '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Reply: INTERACTIVE_TEST_OK"}]}}' | \
QODERCLI_INTEGRATION_MODE=qoder_work QODER_API_ENCRYPTION_BYPASS=true \
QODER_SITE=cn QODER_PERSONAL_ACCESS_TOKEN="" \
"/Applications/QoderWork CN.app/Contents/Resources/bin/qodercli" \
  --storage-dir ~/.qoderworkcn --site cn --model q36fmodel \
  --max-turns 1 --input-format stream-json --output-format stream-json

# 结果：成功返回完整 NDJSON 流
# system.init → system.session_update → assistant → result(success, 1987ms)
```

#### 路径 B 验证记录

| 测试 | 命令 | 结果 |
|------|------|------|
| `machine-info` | `qodercli machine-info` | ✅ 返回 machineToken（88字符）+ machineType（18字符） |
| `get-token` | `qodercli internal get-token --storage-dir ~/.qoderworkcn` | ✅ 返回 access_token |
| `sign-request` (白名单 URL) | `qodercli internal sign-request --url /api/v2/service/pro/imageSearch` | ❌ "not_logged_in" |
| `sign-request` (聊天 URL) | `qodercli internal sign-request --url /api/v2/service/pro/sse/agent_chat_generation` | ❌ "url_not_allowed" |
| access_token 作为 CosyKey | curl + Cosy 签名 | ❌ "Signature invalid" (403) |
| 7 种 payload 变体 | 各种组合 | ❌ 全部 "Signature invalid" |
| QoderCN IDE 凭据 (SharedClientCache) | 签名通过但返回 500/105 | ⚠️ 签名有效但属于不同产品（QoderCN IDE 插件），不可混用 |
