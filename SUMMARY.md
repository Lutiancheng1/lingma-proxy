# lingma-ipc-proxy 项目总结

## 项目概述

**lingma-ipc-proxy** 是一个 Go 后端服务，将 Lingma / QoderCN 的私有协议转换为标准 OpenAI / Anthropic API，使第三方客户端可无缝接入。同时提供 Feishu Bridge 实验功能，将 Lingma 模型能力接入飞书 Bot。

## 双后端模式

| 模式 | 传输 | 适用场景 |
|------|------|----------|
| **Remote**（推荐） | HTTPS → Lingma 远端 API | 无需本地插件，适合 Agent 客户端 |
| **IPC** | WebSocket / Named Pipe → 本地插件 | 复用插件 session 和环境 |

## 核心模块

| 模块 | 职责 |
|------|------|
| `internal/httpapi` | OpenAI + Anthropic 兼容路由 |
| `internal/service` | 请求编排、session 策略、流式输出、fallback |
| `internal/remote` | 远端凭证探测、域名发现、模型列表、chat SSE |
| `internal/lingmaipc` | IPC 传输（WebSocket / Named Pipe） |
| `internal/toolemulation` | Prompt 注入 + Action Block 解析（兼容无原生 tool calling 的模型） |
| `internal/feishu` | Feishu Bridge：Bot 收发、CardKit 流式卡片、lark-cli 集成 |

## Feishu Bridge（实验分支 `feat/feishu-bridge-go`）

通过 `lark-cli` 接入飞书，支持 Bot 消息收发、工具调用（IM/Drive/Calendar）、CardKit 流式卡片打字机效果。

三级降级：CardKit streaming → legacy 整卡 PATCH → 纯 markdown。

**仅通过 Actions artifact 内测，不参与 release 发布。**

## API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/v1/models` | GET | 可用模型列表 |
| `/v1/chat/completions` | POST | OpenAI 格式对话 |
| `/v1/messages` | POST | Anthropic 格式对话 |

## 桌面 App

Wails 桌面应用，提供代理管理、请求历史、token 统计、Feishu Bridge 设置页。

详细架构见 `docs/architecture.md`。
