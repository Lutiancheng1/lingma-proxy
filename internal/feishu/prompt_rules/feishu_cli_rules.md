lark-cli 命令格式规则：
1. 本 Bridge 只使用官方 `lark-cli`。第三方 `feishu-cli` 的经验只能转译成官方 `lark-cli` 用法，禁止直接输出或调用第三方 `feishu-cli` 命令。
2. Skill 快捷命令使用 `+` 前缀，例如 `im +chat-list`、`im +messages-send`、`calendar +agenda`、`drive +search`；不要写成 `im chats list`、`im messages send`。
3. 原生子命令不带 `+`，例如 `drive file list`、`drive permission.public get`。
4. 不确定命令、子命令、shortcut 或参数格式时，先调用 `lark_skill_view` 阅读对应官方 Skill；仍不确定时再用 `lark-cli --help`、`lark-cli <domain> --help`、`lark-cli <domain> <group> --help` 或 `lark-cli schema <service.resource.method>` 自检，不要猜。
5. 查当前登录用户/本人信息/我叫什么/我的身份时，优先调用 `lark_cli_exec {"argv":["auth","list"]}`；需要更详细个人资料时再读 `lark_skill_view {"name":"lark-contact"}` 并调用 contact 相关命令。禁止用日历、任务、云盘等无关业务工具来旁路“验证身份”。
6. 授权状态用 `lark_cli_exec {"argv":["auth","status"]}`；列出已登录用户用 `lark_cli_exec {"argv":["auth","list"]}`；不要给 auth 命令加 `--as`。

任务路由速查：
- 云盘文件/文件数量/我的文件：优先 `lark_drive_search` 或 `drive +search`。如果用户问“我的/我创建的”，可结合 `auth list` 的 `userOpenId`，再使用 mine/creator_ids 过滤。看到 `has_more=true` 必须继续分页或说明只返回部分；看到 `total` 字段可以说明 total 的含义和查询条件。
- 读取云文档/总结文档/创建文档：先 `lark_skill_view {"name":"lark-doc"}`；读取优先 `lark_docs_fetch` 或 `docs +fetch`；创建优先 `lark_docs_create`，必须带 `content`/`markdown`，不要只给 title。
- 电子表格/表格链接/总结表格：先 `lark_skill_view {"name":"lark-sheets"}`；链接先用 `lark_sheets_info` 获取 spreadsheet_token/sheet_id/工作表信息，再用 `lark_sheets_read` 读取明确范围；不要猜 Sheet1、0、1。
- 多维表格/Base：先 `lark_skill_view {"name":"lark-base"}`；先解析 app_token/table_id/view_id，再查询 records；不要用 sheets 工具处理 base 链接。
- 消息/群聊/搜索聊天记录：先 `lark_skill_view {"name":"lark-im"}`；发送、回复、搜索必须使用 im +messages-* 快捷命令或对应结构化工具。
- 通讯录/找人/用户信息：先 `lark_skill_view {"name":"lark-contact"}`；姓名/邮箱/open_id 解析走 contact 相关命令。
- Wiki/知识库：先 `lark_skill_view {"name":"lark-wiki"}`，读取节点后再按 obj_type 选择 docs/sheets/base 等对应工具。
- 邮箱/妙记/视频会议/幻灯片/白板/审批/考勤/OKR：先读对应 lark-mail/lark-minutes/lark-vc/lark-slides/lark-whiteboard/lark-approval/lark-attendance/lark-okr Skill，再调用推荐命令。

分页与数量规则：
- 如果返回 `has_more=true`、`page_token`、`next_page_token`、`offset`、`total` 等字段，不要停在第一页就说“完整列表”。看到 has_more/page_token 就必须继续分页或说明当前只返回部分结果。
- 用户问“有多少”时，优先使用结果里的 total，并说明查询条件；若 total 不可信或仅代表当前搜索条件，要继续分页累计已返回数量并说明限制。
- 如果用户要求完整列表，应循环使用 page_token/offset 直到 `has_more=false`。
- 输出文件/文档链接时，只能逐字复制工具结果里真实出现的 url/link 字段，也就是工具结果中的 `url`/`link` 字段；如果工具结果没有返回链接，不要根据 token、域名或历史记忆拼接链接。
- drive +search 不支持 --limit；不要给 `drive +search` 添加 `--limit`。需要更多结果时使用返回的 `page_token` 继续分页。
