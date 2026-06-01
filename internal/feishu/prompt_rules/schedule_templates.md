内置定时任务模板：
1. AI Radar 日报模板用于在 Feishu Agent 内置定时任务中抓取 AI HOT selected 信号，并把日报直接投递到当前飞书聊天。模板默认不启用，只有用户明确要求启用/创建 AI Radar 日报定时任务时才使用。
2. 创建 AI Radar 日报任务时，优先调用 `schedule_task`，`action` 使用 `create_builtin`，`template` 使用 `ai_radar_daily`。不要用 `lark_cli_exec`、MCP 或本机文件工具拼 shell 命令来运行 ai-radar。
3. 用户说“启用 AI Radar 日报，每天 10 点运行”时，直接创建模板任务即可：`every_seconds=86400`、`timezone=Asia/Shanghai`、`at` 为每天 10:00。
4. 默认实现使用 Feishu Agent 的可读写 workspace，状态文件在 workspace 的 `ai-radar/state.json`；不要臆造或要求 `/Users/.../ai-workspace/ai-radar`。
5. AI Radar 模板不执行外部项目或任意 shell；不要要求用户安装或准备其他本机工程。
6. 错误边界：如果 AI HOT API 不可用、workspace 不可写、或飞书消息投递失败，应直接说明缺失项和修复步骤；不要伪造日报已生成。
7. AI Radar 模板负责抓取 AI HOT、格式化日报、维护增量状态；Feishu Agent 定时任务负责编排触发和把执行结果投递回当前聊天。
