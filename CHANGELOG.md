# Changelog

## Unreleased (target: v1.6.10)

- Documented the Feishu Bridge preview build path in both English and Chinese READMEs, including the branch name and GitHub Actions artifact workflow.
- Added a short Feishu Bridge preview feature summary covering bot routing, Feishu tools, streaming cards, scoped local file access, reminders, and context/tool memory.
- 在中英文 README 补充飞书分支预览包说明，明确分支名、Actions artifact 获取路径以及与主分支正式 Release 的区别。
- 补充飞书分支预览能力简介，覆盖 Bot 接入、飞书工具、流式卡片、本地文件分级授权、定时提醒以及上下文 / 工具记忆。

## v1.6.9 - 2026-05-31

- Fixed desktop log retention so realtime log events and persisted request records keep their full bodies for detail inspection and feedback export.
- Kept the Logs list readable by truncating only the table cell preview to 240 characters while preserving the full message for the detail view.
- 修复桌面日志保留逻辑：实时日志事件和持久化请求记录保留完整正文，方便详情查看和反馈导出。
- 日志列表只在表格预览层截断到 240 字符，详情视图仍展示完整消息。

## v1.6.8 - 2026-05-31

- Hardened Remote API tool calling for Claude Code-style clients: requests that include tools now enable prompt-based tool emulation fallback while still passing through native `tools/tool_choice`.
- Fixed Remote API prompt delivery for emulated tools by sending the full tool contract as the upstream chat message when tool emulation is active, instead of building a prompt that the remote message path ignored.
- Tightened tool instructions for local shell workflows: tool calls should be emitted before explanatory text, `cd` must be combined with the dependent command or replaced by absolute paths, dependent shell commands should not be split across calls, and optional commands such as `tree` should not be assumed available.
- Preserved the default incremental streaming behavior for tool requests; full tool-turn aggregation remains an explicit compatibility mode via `LINGMA_AGGREGATE_TOOL_STREAM=1`.
- Verified the fix with real Claude Code v2.1.158 pointed at a temporary Remote API proxy: the natural request to inspect `/Users/tiancheng/ai-workspace` produced an immediate Bash `tool_use` and completed against the correct directory.
- 加强 Remote API 工具调用兼容：当 Claude Code 这类客户端请求携带 tools 时，代理会启用 prompt tool-emulation 兜底，同时继续透传原生 `tools/tool_choice`。
- 修复 Remote API 工具提示生成后没有真正进入上游消息的问题：启用工具模拟时，完整工具契约会作为上游 chat message 发送。
- 收紧本地 shell 工具提示：需要工具时先输出 tool call；`cd` 必须和后续命令放在同一次调用或改用绝对路径；依赖 shell 命令不能跨调用拆分；不默认假设 `tree` 等可选命令存在。
- 保持 tools 请求默认增量流式输出；如需先内部完成工具轮次再输出最终 `tool_use`，仍通过 `LINGMA_AGGREGATE_TOOL_STREAM=1` 显式开启。
- 已用真实 Claude Code v2.1.158 指向临时 Remote API 代理验证：自然提示检查 `/Users/tiancheng/ai-workspace` 时首轮直接产出 Bash `tool_use`，并基于正确目录结果完成回答。

- Hardened Remote API tool emulation so tool-bearing requests automatically use the injected tool prompt as the upstream chat message, improving Claude Code / QoderWork-style tool routing instead of relying on the raw structured messages alone.
- Kept Anthropic streaming incremental by default while parsing action blocks into standard `tool_use` events; `LINGMA_AGGREGATE_TOOL_STREAM=1` remains available for clients that prefer the old fully aggregated tool turn.
- Improved desktop log and feedback diagnostics by emitting full log entries to the UI, truncating only the visible Logs list row, and preserving full request/response bodies in persisted state for later inspection.
- Added the QoderWork CN integration plan, documenting why direct Gateway API access is impractical and why spawning `qodercli` is the viable integration path.
- 加强 Remote API 工具 emulation：携带 tools 的请求会自动使用已注入工具规则的完整 prompt 作为上游 chat message，减少 Claude Code / QoderWork 类场景只收到原始结构化消息时的漏工具问题。
- Anthropic 流式工具调用默认继续保持增量输出，同时把可解析的 action block 转成标准 `tool_use` 事件；需要旧版“先聚合再输出”行为时仍可设置 `LINGMA_AGGREGATE_TOOL_STREAM=1`。
- 桌面日志和反馈诊断保留更完整现场：事件推送完整日志，Logs 列表仅截断可见行，持久化状态保留完整请求/响应正文，方便后续排查。
- 新增 QoderWork CN 集成方案文档，说明直连 Gateway API 的签名/加密限制，以及通过 spawn `qodercli` 集成的可行路径。

## v1.6.7 - 2026-05-27

- Added complete log detail inspection in the desktop Logs page, including ID-based lookup so same-second log records no longer open the wrong entry.
- Added full-log copy for filtered results and a single continuous range selection/copy workflow, making feedback collection easier without relying on truncated table text.
- Fixed Logs page layout regressions: long messages now wrap inside the table instead of pushing content off-screen, the clear-selection button stays on one line, and detail timestamps use the same relative date format as the list.
- Synced the Dashboard configuration copy and reduced the Models panel height so the first screen keeps only key configuration and avoids extra wrapping.
- Added the Feishu Bridge artifact workflow registration to main so the branch packaging workflow is tracked with the repository.
- 桌面日志页新增完整日志详情，按日志 ID 精准读取，避免同秒多条日志打开到错误记录。
- 新增筛选结果完整复制和单段连续区间复制，收集反馈日志时不再依赖列表里的截断文本。
- 修复日志页布局问题：长消息在表格内换行，不再撑出右侧内容；“清除选择”保持单行；详情弹窗时间使用与列表一致的相对日期格式。
- 同步首页配置说明文案，并调低 Models 面板高度，让首页只保留关键配置且减少换行。
- 主分支纳入 Feishu Bridge artifact workflow 配置，便于后续分支包打包流程随仓库追踪。

## v1.6.6 - 2026-05-25

- Enabled the desktop WebView inspector in packaged release builds, so installed desktop apps keep the right-click `Inspect Element` menu for UI/style/runtime troubleshooting.
- Fixed QoderCN IPC image requests by using QoderCN's native `qodercn:///agent/file?path=...` image URI scheme while preserving `lingma:///agent/file?path=...` compatibility for legacy Lingma runtimes.
- Removed the ineffective IPC image `contextParams` branch and restored image payloads to the `session/prompt` image item shape that both QoderCN and Lingma consume.
- Verified the full IPC image path with `/Users/tiancheng/Pictures/ik2.jpg` on both QoderCN and Lingma runtimes; both returned correct visual descriptions of the coastal rocks / cloudy seascape.
- 正式 Release 桌面包保留 WebView Inspector，安装后的桌面 App 也可以通过右键 `Inspect Element` 排查 UI 样式和运行时问题。
- 修复 QoderCN IPC 图片请求：连接 QoderCN 时使用原生 `qodercn:///agent/file?path=...` 图片 URI，同时保留旧 Lingma 运行时的 `lingma:///agent/file?path=...` 兼容。
- 移除无效的 IPC 图片 `contextParams` 挂载分支，恢复为 QoderCN 与 Lingma 都实际消费的 `session/prompt` image item 结构。
- 已用 `/Users/tiancheng/Pictures/ik2.jpg` 分别验证 QoderCN 与 Lingma 两条 IPC 图片链路，二者均能正确描述海边礁石 / 阴云海景。

## v1.6.5 - 2026-05-22

- Cached the last successful auto-detected Remote API domain and made it the first candidate on the next launch, avoiding repeated slow retries through stale default or runtime-discovered domains.
- Reduced desktop startup model probing cost: startup now uses a single `/v1/models` path for warmup plus model refresh, uses a short probe when cached models already exist, and keeps the full configured warmup timeout for manual model refresh.
- Persisted the last successful model list in desktop state so the Dashboard can show known models immediately after reopening while the background refresh runs.
- Fixed the Logs table header layout so the header background spans the full log panel width while row content keeps its inner padding.
- 缓存上一次自动探测成功的远端 API 域名，并在下次启动时优先使用，避免每次重新打开都先尝试过期默认域名或错误运行时候选导致探测变慢。
- 优化桌面端启动模型探测：启动阶段改为单一路径完成 warmup 与模型刷新；已有模型缓存时使用短探测；手动“探测模型”仍保留设置页配置的完整 warmup 超时。
- 持久化上一次成功的模型列表，重新打开桌面端时仪表盘可以先展示已知模型，再由后台刷新更新。
- 修复日志页表头布局：表头背景铺满整个日志面板宽度，日志内容行继续保留内边距。

## v1.6.4 - 2026-05-21

- Fixed Remote API endpoint auto-detection for enterprise QoderCN/Lingma environments by reading real runtime request logs under shared-client CLI log directories and prioritizing actual chat/model API URLs over default or marketplace hosts.
- Added startup model-list retry and post-warmup refresh in the desktop app so Windows first launch no longer gets stuck with an empty model cache after an early `/v1/models` race.
- Kept explicit `Remote API domain` settings authoritative: manually configured enterprise domains are not overwritten by automatic runtime candidates.
- Included the mainline polish that refreshes README screenshots, keeps the Requests table headers consistent with the Dashboard (`Time / Method / Path / Model / Status / Duration / Size`), and adds a generic Logs table header (`Time / Source / Level / Message`).
- 修复企业版 QoderCN / Lingma 的远端 API 域名自动探测：现在会读取 shared-client CLI 日志里的真实请求，并优先使用真实 chat/model API 域名，不再被默认域名或 marketplace/download URL 覆盖。
- 桌面端启动时模型列表自动探测增加短重试，并在 warmup 成功后补刷新一次，降低 Windows 首次打开后模型列表为空、需要手动探测的概率。
- 显式填写的“远端 API 域名”仍保持最高优先级，自动探测不会覆盖用户手动配置的企业域名。
- 合入主线 UI 文档整理：刷新 README 桌面截图，请求流表头统一为与仪表盘一致的英文列名，并补齐日志页通用表头。

## v1.6.3 - 2026-05-21

- Not released as standalone assets; changes were folded into `v1.6.4`.
- Hardened Remote API candidate discovery for QoderCN/Lingma enterprise domains and added verified fallback probing for missing `kmodel` / `mmodel` list entries instead of blindly adding unavailable models.
- Added JetBrains Windows runtime/config/log candidates and automatic fallback to the next discovered Remote API domain when the first auto-detected candidate cannot list models.
- Changed Remote fallback behavior so configured fallback models can still be tried during generation even when the upstream model list omits their aliases.
- 未单独发布安装包；相关修复合并进入 `v1.6.4`。
- 加强 QoderCN / Lingma 企业域名候选探测，并且对上游模型列表缺失的 `kmodel` / `mmodel` 做真实可用性探测后再展示，不再盲目补模型。
- 补齐 Windows JetBrains 系 IDE 的运行时、配置和日志候选路径；自动探测到的首个域名无法列模型时，会继续尝试后续候选域名。
- Remote fallback 生成链路不再依赖模型列表是否返回对应 alias，配置了兜底模型时仍会在真实生成失败后按顺序尝试。

## v1.6.2 - 2026-05-21

- Added official Linux CLI release assets for `linux_amd64` and `linux_arm64`, plus Linux Desktop `.deb` / `.rpm` release assets for both `linux_amd64` and `linux_arm64` built from the Wails desktop app with nFPM.
- Added a multi-stage Docker image and tag workflow for GHCR (`ghcr.io/lutiancheng1/lingma-proxy:<tag>` and `latest`) without bundling desktop runtime, Node, browser, or local login caches.
- Added explicit Remote API proxy configuration via `--remote-proxy-url`, `LINGMA_REMOTE_PROXY_URL`, JSON `remote_proxy_url`, and the desktop Settings page. Empty proxy config preserves Go's default `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` behavior.
- Added Linux-safe desktop defaults: the Linux desktop package exits on window close instead of hiding without a tray restore path, and Linux session shell defaults to `bash`.
- Documented Docker bind-mount and explicit credential-file startup examples, including the container support boundary that IPC is not guaranteed inside Docker.
- 新增 Linux CLI 官方 release 资产：`linux_amd64` 与 `linux_arm64`，并新增基于 Wails 桌面端和 nFPM 打包的 `linux_amd64` / `linux_arm64` Desktop `.deb` / `.rpm` release 资产。
- 新增多阶段 Docker 镜像与 GHCR 发布链路，镜像标签为 `ghcr.io/lutiancheng1/lingma-proxy:<tag>` 和 `latest`，不内置桌面端、Node、浏览器或本机登录缓存。
- 新增显式远端代理配置：`--remote-proxy-url`、`LINGMA_REMOTE_PROXY_URL`、JSON `remote_proxy_url` 和桌面设置页均可配置；留空时保留 Go 默认的 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 行为。
- 新增 Linux 桌面安全默认行为：Linux 桌面端关闭窗口时直接退出，避免无托盘恢复入口；Linux 会话默认 shell 改为 `bash`。
- 文档补充 Docker bind mount 登录态、显式凭据文件和代理示例，并明确 Docker 场景不承诺 IPC 开箱即用。

## v1.6.1 - 2026-05-21

- Fixed Windows QoderCN credential discovery for dashed `%USERPROFILE%\.qoder-cn` layouts, including `shared_client` cache/config/log/socket candidates.
- Reduced noisy Remote API credential errors: the default error now summarizes missing or incompatible QoderCN/Lingma login caches, while `LINGMA_VERBOSE_CREDENTIAL_ERRORS=1` still exposes full candidate paths for diagnostics.
- Added machine-id fallback discovery from `cli/.auth/id` and JetBrains Lingma `Generated uuid:` log lines, covering IntelliJ IDEA plugin installs that have `cache/user` but no `cache/id`.
- Verified JetBrains Tongyi Lingma plugin compatibility on macOS: Remote API and IPC WebSocket both passed model list, Chat Completions, Responses, and Anthropic Messages.
- Documented the JetBrains plugin support boundary and updated QoderCN/Lingma cache path notes in README and architecture docs.
- 修复 Windows QoderCN 使用 `%USERPROFILE%\.qoder-cn` 目录时的登录态发现，补齐 `shared_client` 下的 cache/config/log/socket 候选路径。
- 精简远端 API 登录态错误：默认只提示未找到或缓存格式不兼容；需要完整路径时可设置 `LINGMA_VERBOSE_CREDENTIAL_ERRORS=1` 重新诊断。
- 增加 `cli/.auth/id` 与 JetBrains Lingma 日志 `Generated uuid:` 的 machine-id 兜底解析，覆盖 IntelliJ IDEA 插件只有 `cache/user`、没有 `cache/id` 的安装形态。
- 已在 macOS 实测 JetBrains 通义灵码插件：Remote API 与 IPC WebSocket 的模型列表、Chat Completions、Responses、Anthropic Messages 均通过。
- 文档补充 JetBrains 插件支持边界，并更新 QoderCN/Lingma 缓存路径说明。

## v1.6.0 - 2026-05-20

- Added QoderCN runtime compatibility for Remote API credential discovery and IPC transport discovery, including QoderCN shared-client cache paths, macOS socket/WebSocket discovery, and QoderCN-first runtime selection when both QoderCN and Lingma runtimes are installed.
- Added dual Lingma / QoderCN desktop branding, QoderCN icon support, and updated user-facing Settings, Models, diagnostics, and runtime-copy text to describe the shared Lingma / QoderCN support boundary.
- Wired the HTTP `/version` endpoint into the repository-level `VERSION` source so API diagnostics now return the real release version instead of a service-name placeholder.
- Documented the runtime compatibility matrix: QoderCN desktop app is fully verified on macOS; QoderCN plus `alibaba-cloud.tongyi-lingma` still prefers QoderCN; VS Code extension-only mode is partial because it does not expose the full `session/new` IPC generation path.
- Verified the full OpenAI / Anthropic endpoint matrix in QoderCN-only and QoderCN-plus-VS-Code-extension coexistence modes, including Chat Completions, Responses, Anthropic Messages, debug requests, access logs, and compatibility aliases.
- Kept repository, module, binary, and config-path naming stable for this release; QoderCN support is introduced as a compatibility expansion rather than a breaking product rename.
- 新增 QoderCN 运行时兼容：远端 API 登录态发现和 IPC 传输探测都支持 QoderCN SharedClientCache、macOS socket/WebSocket，并在 QoderCN 与 Lingma 同时安装时优先选择 QoderCN。
- 桌面端品牌位改为 Lingma / QoderCN 双图标共存，新增 QoderCN 图标支持，并统一设置页、模型页、诊断和运行时文案，明确 Lingma / QoderCN 共享支持边界。
- HTTP `/version` 接入根级 `VERSION` 单一来源，诊断接口现在返回真实发布版本，不再返回服务名占位值。
- 文档补充运行环境兼容矩阵：macOS QoderCN 桌面端已完整验证；QoderCN 与 `alibaba-cloud.tongyi-lingma` 共存时优先 QoderCN；单独 VS Code 扩展模式为部分支持，因为缺少完整的 `session/new` IPC 生成路径。
- 完整验证 QoderCN-only 与 QoderCN + VS Code 扩展共存两种场景下的 OpenAI / Anthropic 接口矩阵，覆盖 Chat Completions、Responses、Anthropic Messages、请求调试、访问日志与兼容别名。
- 本版本保持仓库、Go module、二进制名和配置路径不变；QoderCN 作为兼容能力扩展进入 `1.6.0`，不做破坏性改名。

## v1.5.4 - 2026-05-19

- Fixed desktop model discovery timeout handling so manual model refresh now honors the configured warmup timeout instead of failing after a stale hard-coded 5-second path.
- Removed the extra low-level timeout clamp from remote model listing so the warmup / refresh timeout configured in desktop settings can propagate end-to-end.
- Verified the hotfix build line on top of `v1.5.3`, keeping the packaged desktop release flow intact.
- 修复桌面端模型探测超时链路：手动“刷新模型”现在真正遵循“探测超时秒数”配置，不再走遗留的 5 秒硬编码超时。
- 移除底层模型列表请求的额外固定超时截断，确保设置页里的 warmup / 探测超时能够端到端生效。
- 基于 `v1.5.3` 完成热修验证，桌面端打包和发布链路保持不变。

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
