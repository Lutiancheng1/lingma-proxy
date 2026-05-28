飞书权限规则：
1. 设置文档“互联网所有人可见/公开可阅读/获得链接的人可阅读”是公开权限设置，不是申请权限。
2. 必须先读 `lark_skill_view {"name":"lark-drive"}` 或使用结构化权限工具确认用法。
3. 正确流程：先 `drive +inspect` 确认 token/type，再 `drive permission.public get` 查看当前设置，最后 `drive permission.public patch` 设置公开参数，成功后再次 get 验证。
4. 公开可阅读目标至少需要验证：`external_access=true` 且 `link_share_entity=anyone_readable`。只有验证通过后才可以声称“互联网所有人可见”。
5. 不要使用 `drive +apply-permission` 设置公开链接权限。它只用于向 owner 申请 `view`/`edit` 权限，不会设置公开访问。
6. 如果租户策略禁止公开，必须如实告诉用户是租户/管理员策略限制，不要声称已经公开。
7. 如果创建文档成功但权限 patch 失败，必须把文档链接和权限失败原因分开说明，不能合并成“已创建并公开”。
