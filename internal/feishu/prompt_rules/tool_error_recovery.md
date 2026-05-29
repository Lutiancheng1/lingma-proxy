工具失败恢复协议：
1. 工具失败后先分类，不要立刻换一个猜测命令。
2. `Usage` / `Available Commands` / `unknown command` / `unknown flag`：下一步只允许调用 lark_skill_view、`lark-cli <domain> --help`、`lark-cli <domain> <group> --help` 或 `lark-cli schema`；查到真实用法后再重试业务命令。
3. `validation` / `invalid params` / `invalid value`：下一步先用 `lark-cli schema <service.resource.method>` 或对应 Skill 查参数结构；不要仅替换一个相似参数继续试。
4. `permission` / `missing scope` / `need_user_authorization`：停止业务命令，等待 Agent 授权流程；授权完成后从失败点继续，不要改做无关工具。
5. 如果同一目标连续两次因为命令/参数失败，必须先调用对应 `lark_skill_view` 或 schema；仍不确定时向用户报告阻塞，不要继续盲试。
6. 成功判断必须和目标一致：创建文档成功不等于权限已公开；drive +inspect 成功只说明文档存在；drive +search 成功只说明查到文档；drive +apply-permission 成功/失败都不代表设置了公开链接权限。
7. 完成高风险写操作后必须用只读命令验证：公开权限用 permission.public get，创建文档用 inspect/fetch，表格写入用 read，成员权限用 permission.members/auth 或对应 get/list。

常见失败纠偏：
- 同一个命令失败后，不要原样重复。先读对应 `lark_skill_view` 或调用 `--help` 查真实用法，再换命令。
- 出现 `unknown flag: --as` 时，说明该命令不支持身份参数，去掉 `--as` 后重试；尤其 auth/help/version/config 类命令不要带 `--as`。
- 出现 `drive +apply-permission` 的 perm 只允许 view/edit，说明你正在“申请权限”而不是“设置公开权限”；如果用户目标是互联网公开可阅读，必须改用 `drive permission.public patch`，并用 `permission.public get` 验证。
- 出现 Usage/Available Commands 时，应从 help 输出中选择真实存在的 +shortcut 或子命令重试。
