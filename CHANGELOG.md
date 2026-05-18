# Changelog

## Unreleased (target: v1.5.3)

## v1.5.3 - 2026-05-18

- Replaced fragile native `window.confirm` usage with a shared in-app confirmation dialog for destructive actions and unified the quit-confirm flow between the title-bar power button and the menu `Cmd/Ctrl+Q` path.
- Split debug inspection endpoints so `/debug/requests` returns request inspection records and `/debug/access-logs` returns HTTP access-log summaries; `/debug/logs` and `/api/logs` remain compatibility aliases with explicit access-log semantics.
- Moved the desktop app version to a single repository source via [VERSION](./VERSION), added `scripts/sync-version.sh` and `scripts/check-version-sync.sh`, and added CI drift detection for versioned release-facing files.
- Removed stale desktop dashboard state/quit branches that were no longer wired into the UI, reducing dead logic around health probes and legacy quit confirmation.
- Rebuilt and validated the packaged desktop line as `1.5.3`.
- 把原生 `window.confirm` 替换为统一的应用内确认弹层，请求流清空、日志清空以及顶部电源按钮/菜单 `Cmd+Q` 现在都走同一套确认交互。
- 拆分调试接口语义：`/debug/requests` 只返回请求检查记录，`/debug/access-logs` 返回 HTTP 访问日志摘要；`/debug/logs` 和 `/api/logs` 保留为兼容别名，但语义已经明确为 access log。
- 版本号改为仓库单一来源：新增根级 [VERSION](./VERSION)、`scripts/sync-version.sh`、`scripts/check-version-sync.sh`，并增加 CI 漂移校验，避免 `wails.json`、README、CHANGELOG 再各自手工维护。
- 清理桌面端 Dashboard 中未接线的 health / quit 旧状态机和方法，减少后续在旧分支上再踩回归的概率。
- 桌面端正式版本线提升到 `1.5.3` 并完成本地重建验证。

## v1.5.2 - 2026-05-18

- Tightened desktop-side request/log rendering to keep only summaries in hot UI state and load full request or log bodies on demand.
- Reduced dashboard refresh pressure further by polling lightweight request summaries instead of full request payloads.
- Added in-panel `Cmd/Ctrl+F` search for desktop request/response detail viewers, including scoped search boxes, highlighted matches, and next/previous navigation inside the active content pane.
- Fixed desktop request-stream selection edge cases: same-second requests now use stable UUIDs, Dashboard-to-Requests first-click jump no longer loses selection, and request rows keep a stable “has body / no body” summary to avoid layout jitter.
- Corrected desktop and README model metadata copy: `Qwen3-Coder` now uses a conservative `256k` label, while `Qwen3.6-Plus` and `Qwen3-Thinking` are documented as `1M` and `Qwen3-Max` as `256k` based on the latest verified Bailian-side metadata.
- Bumped the local desktop validation line to `1.5.2`.
- 请求流和日志页进一步收紧为“摘要列表 + 详情按需加载”，避免前端热路径长期持有完整正文。
- 仪表盘继续降载，轮询时优先拉取轻量请求摘要而不是完整请求体。
- 请求内容 / 响应内容区域新增 `Cmd/Ctrl+F` 局部搜索，支持右上角搜索框、命中高亮、上下跳转，并且只作用于当前激活的内容面板。
- 修复请求流选中与跳转细节：同秒请求改用 UUID 防止多条同时高亮；首页最近请求首次跳转到请求流时不再丢失选中；列表稳定保留“包含请求体 / 无请求体”摘要，避免点击后行高抖动。
- 修正文案：模型上下文单位统一为 `256k / 1M` 口径，`Qwen3-Coder` 为保守的 `256k`，`Qwen3.6-Plus`、`Qwen3-Thinking` 为 `1M`，`Qwen3-Max` 为 `256k`。
- 本地桌面端候选版本线提升到 `1.5.2`。

## v1.5.1 - 2026-05-15

- Clarified the Remote API reasoning boundary: the proxy forwards `thinking` / `reasoning` intent, but the current upstream remote SSE does not expose a separate structured reasoning block. This should not be interpreted as “the model did not reason internally”.
- Added a unified IPC reasoning compatibility matrix for Claude Code, Hermes CLI, and Codex CLI, using the same fixed complex probe and explicitly separating protocol-layer capability from client-side rendering.
- Documented per-model IPC reasoning behavior across `Auto`, `Kimi-K2.6`, `MiniMax-M2.7`, `Qwen3-Coder`, `Qwen3-Max`, `Qwen3-Thinking`, and `Qwen3.6-Plus`.
- Confirmed the current safest cross-client IPC recommendation for visible reasoning panels is `Qwen3-Thinking`.
- Rebuilt the desktop app line to `1.5.1` for the next packaged release.
- 收紧 Remote API 模式的 reasoning 文案边界：代理会透传 `thinking` / `reasoning` 请求意图，但当前上游远端 SSE 不会返回独立的结构化 reasoning block；这不应被误解成“模型没有进行内部推理”。
- 增加 Claude Code、Hermes CLI、Codex CLI 三客户端统一的 IPC 思考兼容矩阵，统一使用同一条复杂固定探针，并明确区分“协议层能力”和“客户端展示层行为”。
- 文档化 `Auto`、`Kimi-K2.6`、`MiniMax-M2.7`、`Qwen3-Coder`、`Qwen3-Max`、`Qwen3-Thinking`、`Qwen3.6-Plus` 的逐模型 IPC 思考表现。
- 当前最稳的三客户端统一 IPC 思考推荐模型明确为 `Qwen3-Thinking`。
- 桌面端版本线提升到 `1.5.1`，用于本轮正式打包发布。

## v1.5.0 - 2026-05-14

- Added stable OpenAI Responses API compatibility for Codex CLI, including `/v1/responses` and `/api/v1/responses` streaming/non-streaming support.
- Fixed Codex CLI multi-step tool workflows so project-structure inspection, command execution, file edits, and unified diff responses now complete through the proxy instead of retrying with 502 errors.
- Fixed the Remote API image-context branch so image-bearing Codex requests no longer lose tool emulation after IPC image extraction.
- Verified Codex CLI image input, image + tool follow-up, multi-step tool use, and file-edit + diff flows against Brew-installed `codex-cli 0.130.0`.
- Verified the installed desktop app line `v1.5.0` on `http://127.0.0.1:8095/v1`, including retry recovery after stopping and reopening the desktop app during Codex CLI retries.
- Bumped the desktop app line to `1.5.0` for the next packaged local verification build.
- 增加 OpenAI Responses API 兼容层，补齐 `/v1/responses` 和 `/api/v1/responses` 的流式 / 非流式支持，满足 Codex CLI 接入要求。
- 修复 Codex CLI 多步工具工作流：项目结构读取、命令执行、文件修改和 unified diff 返回现在都能通过代理稳定完成，不再因为事件序列不完整而反复重试 502。
- 修复 Remote API 图片上下文分叉在 IPC 提取图片后丢失 tool-emulation 的问题，带图请求可以继续走后续工具调用。
- 完整验证 Brew 安装版 `codex-cli 0.130.0`：纯文本、图片输入、图片 + 工具后续调用、多步工具调用、文件修改 + diff 全部通过。
- 进一步基于已安装桌面端 `v1.5.0` 和 `http://127.0.0.1:8095/v1` 做回归，验证桌面端重启期间 Codex CLI 的重试恢复链路也可用。
- 桌面端版本线提升到 `1.5.0`，作为下一轮本地打包验证基线。

## v1.4.15-fix1 - 2026-05-13

- Added a dedicated desktop warmup-timeout setting for startup/model-detection flows. The default is now 30 seconds, independent from the main per-request timeout.
- Added `scripts/rebuild-local-app.sh` as the standard local macOS desktop rebuild flow: package -> stop old app -> replace `/Applications` -> reopen.
- Removed the accidentally tracked `lingma-ipc-proxy.macos.json` machine-local config from the repository and ignored it for future commits.
- Ignored the local `.playwright-mcp/` workspace to keep browser-testing artifacts out of Git.
- Clarified license scope and model-availability disclaimers in the README: screenshots and recommended models reflect the maintainer's enterprise Lingma environment and may differ across personal, business, or other enterprise tenants.
- 增加桌面端单独的探测超时秒数配置，默认 30 秒，仅作用于启动代理和手动探测模型，不再与正式请求超时混用。
- 增加 `scripts/rebuild-local-app.sh` 本地标准重建脚本，固定执行“打包 -> 停旧进程 -> 覆盖 `/Applications` -> 重新打开”。
- 删除误提交的 `lingma-ipc-proxy.macos.json` 本机配置文件，并加入忽略规则，避免个人开发机配置继续进入仓库。
- 忽略本地 `.playwright-mcp/` 目录，避免浏览器测试临时目录进入 Git。
- 补充许可证和模型可用性说明：README 中已明确当前截图和推荐模型来自维护者企业版 Lingma 环境，不代表个人账号、商业账号或其他企业租户一定拥有相同模型集合。

## v1.4.15 - 2026-05-13

- Added desktop request-detail jump flow: clicking a recent request on the Dashboard now opens the Requests page, scrolls to the matching record, and expands its full request/response details after data loads.
- Added smarter desktop request timestamps: request tables now show `今天` / `昨天` / `MM/DD HH:mm:ss` instead of time-only values, making cross-day debugging easier.
- Added backward-compatible timestamp recovery for legacy desktop request history that only stored `HH:mm:ss`; if old entries still look wrong after migration, clear request history once and all newly recorded entries will use full timestamps.
- Added a desktop feedback-package export workflow. Users can choose a time range and generate a redacted local ZIP bundle for issue reporting, including app logs, request logs, config summary, environment summary, and detection info without bundling raw credentials.
- Added a dedicated desktop warmup-timeout setting for startup/model-detection flows. The default is now 30 seconds, independent from the main per-request timeout.
- Added `scripts/rebuild-local-app.sh` as the standard local macOS desktop rebuild flow: package -> stop old app -> replace `/Applications` -> reopen.
- Removed the accidentally tracked `lingma-ipc-proxy.macos.json` machine-local config from the repository and ignored it for future commits.
- Clarified license scope and model-availability disclaimers in the README: screenshots and recommended models reflect the maintainer's enterprise Lingma environment and may differ across personal, business, or other enterprise tenants.
- Refined Dashboard health metrics with explicit `ms` / `s` / `min` units, restored `Avg / P50 / P95 / Max` labels, and hover explanations for each latency statistic.
- 增加桌面端请求详情跳转能力：点击首页最近请求可直接打开请求流页面，自动滚动并展开对应记录，减少手动查找路径。
- 增加桌面端请求时间智能格式化：请求列表改为显示“今天 / 昨天 / 月日+时间”，跨天排查时不再只有裸时间。
- 增加旧桌面请求历史的时间兼容修复：对只保存 `HH:mm:ss` 的旧记录做回填；如果历史记录迁移后仍不准确，清空一次请求记录，后续新记录会完整保存时间戳并稳定显示日期。
- 增加桌面端“反馈日志包导出”功能：用户可选择时间范围，一键生成本地脱敏 ZIP 反馈包，包含应用日志、请求日志、配置摘要、运行环境与探测信息，默认不打包明文登录态和无限长原始请求体。
- 增加桌面端单独的探测超时秒数配置，默认 30 秒，仅作用于启动代理和手动探测模型，不再与正式请求超时混用。
- 增加 `scripts/rebuild-local-app.sh` 本地标准重建脚本，固定执行“打包 -> 停旧进程 -> 覆盖 `/Applications` -> 重新打开”，避免桌面端旧进程残留导致覆盖不彻底。
- 删除误提交的 `lingma-ipc-proxy.macos.json` 本机配置文件，并加入忽略规则，避免个人开发机配置继续进入仓库。
- 补充许可证和模型可用性说明：README 中已明确当前截图和推荐模型来自维护者企业版 Lingma 环境，不代表个人账号、商业账号或其他企业租户一定拥有相同模型集合。
- 优化首页健康指标显示：延迟数值改为明确的 `ms / s / min` 单位，恢复 `Avg / P50 / P95 / Max` 标签，并提供悬浮解释说明。

## v1.4.13 - 2026-05-12

- Fixed desktop Dashboard token statistics when third-party clients return flat token fields such as `prompt_tokens`, `completion_tokens`, and `total_tokens` without wrapping them inside a `usage` object.
- 修复桌面端首页 Token 统计在第三方客户端返回平铺 token 字段时显示为 0 的问题；现在即使没有 `usage` 包裹，也会正确累计 `prompt_tokens`、`completion_tokens` 和 `total_tokens`。
- Added desktop regression coverage for standard usage-wrapped responses, flat token responses, and SSE `data:` events carrying flat token fields.
- 增加桌面端回归测试，覆盖标准 `usage` 结构、平铺 token 结构，以及 SSE `data:` 事件中的平铺 token 字段，避免后续兼容再次回退。

## v1.4.12 - 2026-05-08

- Fixed OpenClaw-style image requests where the prompt is sent as a short OpenAI `system` message and the user message contains only `image_url`.
- Added regression coverage for image-only user turns with prompt fallback from short system instructions.
- Verified Hermes CLI `--image`, OpenClaw `infer image describe --file`, and OpenClaw agent sandbox image-marker flows through Lingma Proxy.
- Updated the image compatibility documentation with the tested Hermes/OpenClaw behavior and the OpenClaw sandbox file-delivery limitation.

## v1.4.11 - 2026-05-08

- Fixed Claude Code image paste requests in Remote API mode when the request also includes tools and long conversation history.
- Remote image + tools requests now extract image context through IPC using only the latest image-bearing user turn, preventing stale project context from making the model answer as if it cannot see the image.
- Added regression coverage for compact image-context extraction while preserving normal Remote native tool handling.
- Documented the tested image compatibility matrix for OpenAI image URLs, Anthropic image blocks, Claude Code pasted images, and expected Hermes / OpenClaw compatibility boundaries.

## v1.4.10 - 2026-05-08

- Fixed a streaming regression introduced in v1.4.9: requests with `tools` now stream incrementally by default instead of being aggregated until the full response is complete.
- Kept `LINGMA_AGGREGATE_TOOL_STREAM=1` as an explicit compatibility switch for clients that need full aggregation before tool-call emission.
- Added regression coverage for tool-stream aggregation opt-in behavior.
- Verified OpenAI and Anthropic streaming endpoints with tool schemas return incremental text deltas.
- Added an IPC setup guard for image requests: if the Lingma app/plugin has been fully exited and `session/new` no longer responds, the proxy now fails fast with a clear reopen-Lingma hint instead of hanging until the client times out.

## v1.4.9 - 2026-05-07

- Added Remote-mode image routing: image requests now use the proven Lingma IPC image pipeline instead of sending local/data URLs directly to the remote chat endpoint.
- Added mixed image + tool handling: the proxy extracts image context through IPC, then returns to Remote API native tool calling so clients still receive proper `tool_calls` / `tool_use`.
- Fixed multi-turn image follow-ups by reusing the most recent user image from request history when the latest user turn says things like "continue based on the previous image".
- Improved Remote API tool compatibility by forwarding structured messages, tool definitions, tool choice, and native remote tool-call deltas instead of prompt-emulating tools in Remote mode.
- Added regression tests for remote structured tools, image routing, image-context injection, and previous-turn image reuse.
- Verified the production desktop app launch path from `/Applications/Lingma Proxy.app`, including pure image, multi-turn image, and image + forced tool-call requests.

## v1.4.8 - 2026-05-06

- Fixed Remote API base URL auto-detection so Lingma OSS/static asset hosts are rejected and cannot be used as API endpoints.
- Improved Remote API model-list 404 errors with a clear hint to manually set the official or enterprise remote API domain.
- Restored desktop input editing shortcuts by using the native Wails edit menu, fixing copy, paste, cut, undo, redo, and select-all in app input fields.
- Added regression tests for Windows/Lingma log URL parsing, missing leading `h` repair, and OSS-host rejection.

## v1.4.7 - 2026-05-06

- Renamed user-facing product, desktop app, release assets, and documentation from Lingma IPC Proxy to Lingma Proxy.
- Clarified that Remote API mode is the recommended default and that only IPC plugin mode is based on the `coolxll/lingma-ipc-proxy` protocol discovery.
- Added `lingma-proxy.json` and `~/.config/lingma-proxy/config.json` config lookup/write paths while keeping legacy `lingma-ipc-proxy` config fallback.
- Added a desktop top-bar force quit button that stops the proxy and exits the app on macOS and Windows.
- Added Anthropic `/v1/messages/count_tokens` compatibility for Claude Code v2.1.129+.
- Reduced prompt-emulated tool loops by allowing final answers after tool results and dropping tool calls with missing required arguments.
- Prevented hosted Anthropic `web_search` from being short-circuited again after a `tool_result` follow-up.
- Changed the default proxy request timeout to `0`, meaning no proxy-level per-request deadline. Positive timeout values still enable timeout-triggered remote fallback.

## v1.4.6 - 2026-05-06

- Added the VS Code Lingma plugin shared cache directory `~/.lingma/vscode/sharedClientCache` to remote credential auto-detection.
- This fixes Windows setups where Lingma is installed through the VS Code extension and stores `cache/user` plus `cache/id` under the plugin shared client cache.

## v1.4.5 - 2026-05-06

- Improved Windows remote credential detection for Lingma App installations.
- Remote API mode now checks `cache/user` before machine-id lookup so missing-login errors are more accurate.
- Expanded machine-id discovery to recursive Lingma app logs and VS Code Lingma plugin logs instead of only `logs/lingma.log`.
- Added support for additional machine-id log formats such as `machine_id`, `machineId`, and JSON-style fields.

## v1.4.4 - 2026-05-05

- Enabled real SSE streaming for OpenAI `/v1/chat/completions` and Anthropic `/v1/messages` requests that include tools.
- Added a tool-stream filter so normal text can stream immediately while prompt-emulated action blocks are buffered and emitted as proper `tool_calls` / `tool_use` events at the end.
- Added `LINGMA_AGGREGATE_TOOL_STREAM=1` as a compatibility switch to restore the previous aggregate output behavior for tool requests.
- Tightened tool-emulation instructions so conceptual chat and explanation requests do not trigger unnecessary terminal/tool calls.
- Added tests for hosted Anthropic web search handling, tool-stream filtering, and updated tool prompt guidance.

## v1.4.3 - 2026-04-30

- Added remote API timeout fallback with a configurable model order. The default order is Kimi-K2.6, MiniMax-M2.7, Qwen3-Coder, Qwen3.6-Plus, Qwen3-Max, and Qwen3-Thinking.
- Fallback only runs before any streaming bytes are sent and only uses models returned by the active `/v1/models` response.
- Changed the default request timeout from 120 seconds to 300 seconds.
- Added a desktop Settings switch and fallback model list editor.
- Added persistent desktop app state for request history, app logs, and cumulative token usage.
- Added a Dashboard token usage card and model-list specification chips for context window and capability summaries.
- Added model display to the desktop request stream table and model-aware request search.
- Fixed Dashboard "recent model" tracking so health/model-list requests no longer override the last real chat model.
- Updated architecture documentation to cover the IPC and Remote API dual-backend design.
- Disabled desktop Inspector and default context menu in production builds; local development can opt in with `LINGMA_DESKTOP_DEBUG=1`.

## v1.4.2 - 2026-04-30

- Default backend changed to remote API mode for new CLI and desktop configurations.
- Default model changed to `kmodel` (`Kimi-K2.6` in Lingma remote model list).
- Removed the proxy-injected fake `Auto` model in remote mode so the model list only shows models returned by Lingma.
- Fixed Dashboard recent requests showing `MiniMax-M2.7` for model discovery and health/debug requests that do not contain a model field.
- Added request record model and payload size fields for the desktop app request table.
- Updated Dashboard transport display to show `Remote API` when remote backend is active.
- Updated Hermes local config to use Lingma Proxy with `kmodel` and remote model IDs.
- Updated README / README.zh-CN for remote-first mode, Kimi recommendation, package selection, protocol support, and debug/log endpoints.

## v1.4.1 - 2026-04-30

- Improved remote enterprise endpoint detection from Lingma logs.
- Added support for showing detected remote base URL and credential source in desktop Settings.
- Added macOS DMG packaging in GitHub Actions.

## v1.4.0 - 2026-04-30

- Added experimental remote API backend alongside the original IPC plugin backend.
- Added remote credential import from local Lingma login cache or explicit credential files.
- Added OpenAI / Anthropic compatible routing over the remote backend.
- Added request and log debug endpoints for troubleshooting.
