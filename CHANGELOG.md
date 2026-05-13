# Changelog

## Unreleased

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
