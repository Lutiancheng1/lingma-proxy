# Lingma Feishu Agent 食用指南

> 本文档对应飞书云盘中的云端源文档（链接：https://www.feishu.cn/docx/BwacdC9evoNa1txuGUMcFVChnHd，Token: BwacdC9evoNa1txuGUMcFVChnHd）。修改时请确保与云端同步，勿直接在此处进行非同步性修改。

本指南将指引您从零开始完成 **Lingma Feishu Agent** 的下载、安装、配置和飞书交互，帮助您将 AI Agent 无缝接入飞书办公生态。

Tips: 下文图中 bridge 字段为老版本留存 正不断迭代为新版agent字段, 叫法不一样 内容功能均一致。

---

## 1. 前置条件

在启动 Lingma Feishu Agent 之前，请确保具备以下前置条件：

### 1.1 本地 AI 后端运行时

- **推荐**：保持本地 **QoderCN 桌面 App** 或 **通义灵码 (Tongyi Lingma) IDE** 处于开启且已登录状态。
- **说明**：Agent 将自动探测这些本地运行时的 WebSocket/Named Pipe 通道，从而复用您的登录凭证和模型授权。

### 1.2 系统环境要求（Node.js）

- **版本要求**：系统需具备 Node.js 运行环境，版本要求 >= 20.12。
- **自动检测与协助**：桌面 App 首次运行或打开设置页面时会自动检测系统的 Node.js、npm 及 npx。若版本不足或未安装，桌面 App 会在自检面板提示，并尝试调用本机已有的包管理器（macOS 上的 nvm/fnm/volta/Homebrew，Windows 上的 winget/nvm-windows）协助您完成自动安装；若系统无包管理器，您需手动在本地安装符合版本的 Node.js 后点击“刷新状态”。

---

## 2. 下载与安装

由于智能体当前仍处于快速迭代升级阶段，为您提供当前最新的稳定版安装包进行下载安装：

### 2.1 下载与安装

1. 前往官网下载安装最新版本：<https://lingma-feishu-agent.pages.dev/download>
2. 按页面提示下载对应平台安装包并完成安装。
3. macOS 下载 DMG 后拖入 `Applications`；Windows 下载 ZIP 后解压并运行 `Lingma Proxy.exe`。

---

## 3. 飞书 Agent 初始化与配置

桌面 App 中的代理路由服务在打开应用时即会在本地 `http://127.0.0.1:8095` 默认自动启动，但飞书 Agent 服务需要通过以下步骤完成模型连通、环境自检和授权，才能正式启用：

### 3.1 步骤一：探测模型与上游地址校验

在使用飞书 Agent 之前，首要任务是确保代理服务可以成功连接您的上游 AI 模型运行时。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=Y2IyNDAxNDQzMGVkMjAwN2JlYmZkNGU1ZWExOTY0MDRfNmE3NWI1YjdkNmQ0NzIyMDU0MDM1YjJkMGI5YWM3NmVfSUQ6NzY0NTE4MjkzNjY4NTQ5NzI4MF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 打开桌面 App，在主界面点击 **"探测模型"** 按钮。
2. **正常情况**：如果下方的“模型列表”能够顺利拉取并展示可用的模型（如 `Kimi-K2.6`、`Qwen3-Thinking` 等），说明本地代理与您的本地 AI 运行时已连通成功。此时，您**不需要**额外去关注或手动修改设置面板中的“远端 API”或“上游代理”地址。
3. **异常情况**：一旦点击“探测模型”后发现模型列表为空、报错或探测不出来，说明本地代理与上游网关连接受阻。此时，您必须进入左侧导航栏的 **"设置" (Settings)** 页面，检查您所配的远端 API 服务地址，并核实该地址是否与您实际账号所对应的上游 API 服务地址保持一致。

<grid>
<column width-ratio="0.512693">
![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=MTZiMDY2ZGU1YjAyMjUyZDRkMmE2M2MwMGU4YTJhMzVfNTIyNzAwMjFmNTIxODY2MzRhOGUwYjBjZDY1Mjc5ZGFfSUQ6NzY0NTE4MzA5MTY0MDAyODEzMl8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)
</column>
<column width-ratio="0.487307">
![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=OGEyNjc4MjEwNGFkMmNhZGQwMTA3NmIxMjA3NGJkNjVfMmJiODgyM2VkNTIwMDA1YWEzYzhjNWQ3NWU2YzFkNGNfSUQ6NzY0NTE4MzMwNDQ5NTg3NzA4MV8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)
</column>
</grid>

如发现探测出来不一致 可手动指定 然后保存 并重启 

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=ODkwNDFkMjk4OWFiNzZjY2I1YzU2ZjYxZTc0NWE4M2NfYTlhYjNmZTZhYTQ2OWZkMTE0YzBjZTkxNGE0ODdmYmZfSUQ6NzY0NTE4Mzg2OTk2OTU4MzMxOF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

### 3.2 步骤二：安装 CLI 与 Skills

部署官方飞书 CLI 客户端及所需的技能工具包。

1. 进入左侧导航栏的 **"设置" (Settings)**。
2. 在 Feishu Agent 的环境检测步骤面板中，如果提示 CLI 未就绪，点击此步骤对应的 **"安装"** 按钮。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=MGNjMzVlZjI2ZThlOGY1MWU4YTI5YmVkYmFhODk3MWVfNDlkMmU3N2I0Y2U3NjQ3NjQ2MGMyYTJkOTAyYTA4MjNfSUQ6NzY0NTE4NTY4ODI1NDM1MjU5OF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. App 会在后台自动调用 `npm` 和 `npx` 下载安装飞书 CLI（`@larksuite/cli`）及 Skills 依赖，并在状态栏中实时显示安装日志。若系统全局目录不可写，App 会自动安全降级安装至用户个人应用配置目录，无需管理员权限。
2. 完成后稍等片刻 状态同步后会显示已完成 从而进行下一步

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=ZTcyYmFiYTJlYTg3N2NkYmE5MDFmNGRiNjhkMjYyMDVfYzk0MTlkODBlNmJiMDJhOTRhNGE2MzczNDMxNjY4YjdfSUQ6NzY0NTE4NjE3MjcwNDE5NzgxMl8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

### 3.3 步骤三：初始化飞书应用

在飞书开放平台为 Agent 创建并注册云端应用。

1. 在自检面板第二步点击 **"首次初始化（推荐）"** 按钮。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=NTViMDBlMjFjYWZhNGMwZmIwODk5YzAzNzc2ZTgwZmNfZTlkZjMzNDRhNjAyZGViZDhlNzljYjZiYjMxOTdkMDVfSUQ6NzY0NTE4NjIzOTY3MDc4MzE1Nl8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. App 会自动调用飞书 CLI 在APP下方打开初始化链接, 点就后在默认浏览器中打开飞书应用初始化配置页面。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=MWMyZTY2N2RiZDQwMjIxYTdlZmFmZDVmOTZiOTBhYTVfMDhmZGI0YjRkNzMyMWJlZDg5ZmU5MWFiNWU0YjMwOWZfSUQ6NzY0NTE4NjUwOTczODAwMzY1NV8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 根据浏览器页面的引导，完成您的飞书应用创建或选择已有应用，使其与本地 Agent 完成映射。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=YzEzNjk3ZWUxYzQ4OTUwYzEwM2MzMTkxYTlkMzk4ZDlfNzU3NmRiYTJjZTA4ZjkzNTk2Yjc3OGU2YzI4ZWEzOTNfSUQ6NzY0NTE4ODE5NTExNDM1NTY1MF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 授权完毕后稍等片刻待状态同步后 即可执行下一步

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=ZDRlNDk3NmY2MTU4ZjI4YjRkZDRlZTAzYmZlNWEzZThfZDUwOTU1ZjcyOTgxMWI0NWFkMDllY2JlMDZmZDJmZmNfSUQ6NzY0NTE4ODc4NTE4NTkxODE3M18xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

### 3.5 启用 Agent 服务

1. 当上述环境自检与授权全部配置完成后，在 Feishu Agent 配置区勾选 **"启用 Feishu Agent"** 总开关。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=YmE5YjBmNGJlNzA0YjE0MTE1NjYwNWRjYzBiYzZjZmVfZTFhZGRlY2FjNWMzYWQ4YWEzNjEzYzBkZTRlMDU5ZmFfSUQ6NzY0NTE4OTA1NjgzMzYwNDU1OF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 在“高级设置”中可配置您的 **Bot 名称**（最大 40 字符），此名称将显示在飞书聊天卡片的“正在思考”状态中。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=MmY0NDdjZTA2NmFiMGFmYWMwMzkyNGFiZThjNTZiYWRfMDlmNTdlZWFkMTlhY2JhMzc4OTAyYzA3Y2Y1ZDZmYjBfSUQ6NzY0NTE4OTY5MTE4MDc5Njg4MV8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 点击底部的 **"保存配置"**。应用会自动重启服务以激活 Agent。自此，Agent 开始监听您的飞书群聊并随时待命。
2. 回到飞书app中  在工作台中 找到对应创建开启的应用, 即可开始使用。

<grid>
<column width-ratio="0.358455">
![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=NWY3ZDE5YmMwZDg5ZGUzODM3NzhjMTRiMjQ5YWUxY2FfZGY3MmM4ZTVmNDQyMzI4NzFjNDViMjU4OGE5OGY4YmFfSUQ6NzY0NTE5MDI2NDA4NTEyMTk5MF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)
</column>
<column width-ratio="0.641545">
![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=YmNmNDNkMTZmYWZmZjk2MmRhYWEyNDViMjZlOTMzMjRfMTc2MDRlOWI4MjRiMTNiYzY5Nzk5ZWQ5ODZhMjk1NGVfSUQ6NzY0NTE5MDQzNzY5ODk5NzIwNl8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)
</column>
</grid>



---

## 4. 动态设备流安全登录 (慢登录场景)

在日常使用过程中，如果遇到了长期未活动导致登录态失效，或者在聊天中向 Agent 发送需要更高权限的操作（例如“查看我今天的日程”）时：

1. 大模型本身物理上无法代替您操作凭证，底层的 Agent 安全网关会拦截当前请求，并自动发起设备流登录（`lark-cli auth login --recommend`）。
2. Agent 会在飞书聊天窗口中直接向您发送一条动态授权卡片，其中包含专属的登录链接或登录二维码。
3. 您点击卡片链接在浏览器中完成授权后，Agent 会在后台立即检测到登录成功，并自动恢复执行刚才被阻断的提问，无需您再次重复发送命令。

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=MDE5ZDNmZTVhODNmMGRiMzU3OTdiM2NlOGZlODVmYTVfN2M5ODI4NDI2NmMyNGI4OGY1MjUzZmJjMDZiNzgzZjFfSUQ6NzY0NTE5MTcyMDE3MDI3NzgxNl8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)

1. 如不想每次都需要申请权限可复用我这套权限配置json 在飞书控制台手动倒入授权进行保存(仅授权了某些场景,并非完整), 保存并且更新应用权限后生效。

[开发者后台 - 飞书开放平台](https://open.feishu.cn/app)

![](https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/authcode/?code=ZmMyNTIzNDVmYTY2ZjE3NWZjNDgwODMyNGFiYjYyZTlfNjQ0Y2Y4YzFhYTliMjg5MmJjZWUyYTRiODZjODU3ZTdfSUQ6NzY0NTE5MjY3NjE4Nzg2ODExMF8xNzgwMDQxMzQwOjE3ODAwNDQ5NDBfVjM)



```Plain Text
{
  "scopes": {
    "tenant": [
      "aily:file:read",
      "application:application:self_manage",
      "application:bot.menu:write",
      "cardkit:card:read",
      "cardkit:card:write",
      "contact:contact.base:readonly",
      "docs:document.comment:create",
      "docs:document.comment:delete",
      "docs:document.comment:read",
      "docs:document.comment:update",
      "docs:document.comment:write_only",
      "docx:document.block:convert",
      "docx:document:create",
      "docx:document:readonly",
      "docx:document:write_only",
      "drive:drive.metadata:readonly",
      "im:chat.members:bot_access",
      "im:chat:create",
      "im:chat:read",
      "im:chat:update",
      "im:message",
      "im:message.group_at_msg.include_bot:readonly",
      "im:message.group_at_msg:readonly",
      "im:message.p2p_msg:readonly",
      "im:message.pins:read",
      "im:message.pins:write_only",
      "im:message.reactions:read",
      "im:message.reactions:write_only",
      "im:message:readonly",
      "im:message:send_as_bot",
      "im:message:send_multi_users",
      "im:message:send_sys_msg",
      "im:message:update",
      "im:resource"
    ],
    "user": [
      "aily:session:read",
      "approval:instance:read",
      "approval:instance:write",
      "approval:task:read",
      "approval:task:write",
      "base:app:copy",
      "base:app:create",
      "base:app:read",
      "base:app:update",
      "base:dashboard:create",
      "base:dashboard:delete",
      "base:dashboard:read",
      "base:dashboard:update",
      "base:field:create",
      "base:field:delete",
      "base:field:read",
      "base:field:update",
      "base:form:create",
      "base:form:delete",
      "base:form:read",
      "base:form:update",
      "base:history:read",
      "base:record:create",
      "base:record:delete",
      "base:record:read",
      "base:record:update",
      "base:role:create",
      "base:role:delete",
      "base:role:read",
      "base:role:update",
      "base:table:create",
      "base:table:delete",
      "base:table:read",
      "base:table:update",
      "base:view:read",
      "base:view:write_only",
      "base:workflow:create",
      "base:workflow:delete",
      "base:workflow:read",
      "base:workflow:update",
      "base:workspace:list",
      "board:whiteboard:node:create",
      "board:whiteboard:node:delete",
      "board:whiteboard:node:read",
      "calendar:calendar.event:create",
      "calendar:calendar.event:delete",
      "calendar:calendar.event:read",
      "calendar:calendar.event:update",
      "calendar:calendar.free_busy:read",
      "calendar:calendar:create",
      "calendar:calendar:delete",
      "calendar:calendar:read",
      "calendar:calendar:update",
      "contact:user.base:readonly",
      "contact:user.basic_profile:readonly",
      "contact:user:search",
      "docs:document.comment:create",
      "docs:document.comment:delete",
      "docs:document.comment:read",
      "docs:document.comment:update",
      "docs:document.comment:write_only",
      "docs:document.content:read",
      "docs:document.media:download",
      "docs:document.media:upload",
      "docs:document:copy",
      "docs:document:export",
      "docs:document:import",
      "docs:event:subscribe",
      "docs:permission.member:apply",
      "docs:permission.member:auth",
      "docs:permission.member:create",
      "docs:permission.member:transfer",
      "docs:permission.setting:read",
      "docs:permission.setting:write_only",
      "docs:secure_label:write_only",
      "docx:document:create",
      "docx:document:readonly",
      "docx:document:write_only",
      "drive:drive.metadata:readonly",
      "drive:file.meta.sec_label.read_only",
      "drive:file:download",
      "drive:file:upload",
      "drive:file:view_record:readonly",
      "drive:quota_detail:read_one",
      "im:chat.members:read",
      "im:chat.members:write_only",
      "im:chat:read",
      "im:chat:update",
      "im:feed.flag:read",
      "im:feed.flag:write",
      "im:message",
      "im:message.group_msg:get_as_user",
      "im:message.p2p_msg:get_as_user",
      "im:message.pins:read",
      "im:message.pins:write_only",
      "im:message.reactions:read",
      "im:message.reactions:write_only",
      "im:message.send_as_user",
      "im:message:readonly",
      "mail:event",
      "mail:user_mailbox.mail_contact:read",
      "mail:user_mailbox.mail_contact:write",
      "mail:user_mailbox.message.address:read",
      "mail:user_mailbox.message.body:read",
      "mail:user_mailbox.message.subject:read",
      "mail:user_mailbox.message:modify",
      "mail:user_mailbox.message:readonly",
      "mail:user_mailbox:readonly",
      "minutes:minutes.search:read",
      "minutes:minutes.upload:write",
      "offline_access",
      "search:docs:read",
      "search:message",
      "sheets:spreadsheet.meta:read",
      "sheets:spreadsheet.meta:write_only",
      "sheets:spreadsheet:create",
      "sheets:spreadsheet:read",
      "sheets:spreadsheet:write_only",
      "slides:presentation:create",
      "slides:presentation:read",
      "slides:presentation:update",
      "slides:presentation:write_only",
      "space:document:delete",
      "space:document:move",
      "space:document:retrieve",
      "space:document:shortcut",
      "space:folder:create",
      "spark:app:read",
      "spark:app:write",
      "task:attachment:write",
      "task:comment:write",
      "task:custom_field:read",
      "task:custom_field:write",
      "task:section:read",
      "task:section:write",
      "task:task:read",
      "task:task:write",
      "task:tasklist:read",
      "task:tasklist:write",
      "vc:meeting.meetingevent:read",
      "vc:meeting.search:read",
      "vc:note:read",
      "vc:record:readonly",
      "wiki:member:create",
      "wiki:member:retrieve",
      "wiki:member:update",
      "wiki:node:copy",
      "wiki:node:create",
      "wiki:node:move",
      "wiki:node:read",
      "wiki:node:retrieve",
      "wiki:node:update",
      "wiki:space:read",
      "wiki:space:retrieve",
      "wiki:space:write_only",
      "wiki:wiki:readonly"
    ]
  }
}
```



## 5. 安全授权与分级路径管理 (核心食用指南)

为了防止 AI 读写不安全的文件或发生误删，本 Agent 实施了严密的安全沙盒和分级权限白名单机制。

### 5.1 权限规则矩阵

| 授权范围 | 默认读写权限级 | 授权方式 | 说明 |
|-|-|-|-|
| **应用 Workspace** | 可读、可写、不可删目录 | 默认放行 | 本项目所在的目录，默认对 Agent 完全开放 |
| **系统 /tmp 目录** | 可读、可写 | 默认放行 | 仅限于临时文件存放 |
| **\~/.lark-cli** | 只读 | 默认放行 | 常用配置路径 |
| **自定义路径** | 无权限（默认拦截） | 需用户授权 | 任何其他硬盘目录或文件 |

### 5.2 分级路径授权方式

对于默认拦截的自定义外部路径，可以通过以下两种方式授权：

#### 方法 A：在桌面 App 高级设置中静态配置 (推荐)

1. 进入桌面端 **"设置" (Settings)**，展开高级设置。
2. 在 "授权文件路径" 配置项中，添加您需要授权的本地目录。
3. 您可以为每一条路径配置分级的权限水位线：

   - `read`：仅允许 Agent 读取该目录下的文件内容。
   - `write`：允许 Agent 读取并在该目录下新建/修改文件。
   - `delete`：允许 Agent 对该目录下的文件执行删除操作。
4. 点击保存配置。

#### 方法 B：在飞书聊天中口述动态授权

1. 当大模型需要访问未授权的路径时，会输出包含类似下方的安全报错：

   > `[error] 拒绝访问：路径 /Users/username/my-secret-dir 尚未获得授权。`
2. 此时，您只需在对话框中口述发送：

   > `"授权目录 /Users/username/my-secret-dir"` 或 `"授权文件 /Users/username/my-secret-dir/rules.txt"`
3. 系统将自动将该路径加入白名单，**通过口述方式授权的目录仅拥有只读 (`read`) 权限**，无法写入或删除，以保障最基本的物理安全。*[图片占位符：飞书群聊中口述动态授权路径的交互过程截图]*

### 5.3 文件覆盖与删除的双重物理保护

即使目录已在高级设置中被赋予了写入或删除权限，在敏感操作执行前，Agent 仍会实施最终的物理验证确认：

1. **覆盖文件**：当要修改或重写一个已存在的文件时，Agent 会提示您进行确认。您必须在最新的飞书对话中明确回复 **`确认覆盖 <文件名或绝对路径>`**，方能执行修改。
2. **删除文件**：文件删除（`safe_file_delete`）工具仅支持删除文件，**物理禁止直接删除任何目录**。执行时，不仅模型需要将 confirmed 参数设为 true，Go 后端还会审查您的最新消息。您必须发送 **`确认删除 <文件名或绝对路径>`** 且消息中包含明确的删除指令时，拦截器才会真正放行物理删除。*[图片占位符：飞书群聊中对删除高危操作进行确认与放行的过程截图]*

---

## 6. MCP 外部工具扩展

如果您希望将第三方的外部工具（如数据库查询、搜索引擎、特定的 CLI 脚本等）接入飞书 Agent，可以使用 MCP 协议：

1. 在桌面端点击 **"设置" (Settings)** 下的 **"自定义 MCP JSON"** 按钮。
2. 弹窗中会展示应用专用的 MCP 配置文件，您可以在其中以标准 JSON 结构定义外部的 stdio MCP 服务。例如：

   ```json
   {
     "mcpServers": {
       "sqlite-helper": {
         "command": "node",
         "args": ["/path/to/sqlite-mcp-server/index.js"]
       }
     }
   }
   ```
3. 点击保存。应用将自动初始化该 MCP Server。您可以在设置页面的列表中展开该服务器，查看实际转换并注入给模型的 `mcp__sqlite-helper__query` 等动态工具名称。*[图片占位符：MCP JSON 编辑弹窗和展开展示的工具芯片截图]*

---

## 7. 定时任务 (Scheduler) 食用方法

您可以让 Agent 在后台静默干活，或者定期将统计结果投递给您。

### 7.1 在群聊中直接创建任务

大模型内置了定时任务创建工具，当您对它发送 "每天早上 9 点帮我生成站会报告并发送到群里" 时，它会自动调用 `schedule_task` 工具在本地 SQLite 数据库中保存任务。

- **一次性延迟任务示例**：

  > "5分钟后提醒我检查部署状态"
- **循环 Cron 任务示例**：

  > "每周五下午 5 点自动导出本周的任务列表"
- **静默后台运行模式**：  
如果希望任务悄悄执行而不打扰群聊，可以在要求中加入 `[SILENT]` 标记，执行结果将只记录在本地日志中，不会投递回飞书聊天。

### 7.2 任务管理指令

您可以直接在飞书聊天框中通过以 `/schedule` 开头的斜杠命令对任务进行维护：

- **列出当前会话的所有任务**：

  ```text
  /schedule list
  ```
- **暂停某个任务**：

  ```text
  /schedule pause <task_id>
  ```
- **恢复某个任务**,

  ```text
  /schedule resume <task_id>
  ```
- **删除任务**：

  ```text
  /schedule delete <task_id>
  ```
- **手动立即触发执行一次**：

  ```text
  /schedule run <task_id>
  ```

---

## 8. 常见故障排查

### 8.1 飞书卡片一直卡在 "正在思考" 状态

- **原因分析**：大模型对部分视觉图片或复杂的外部工具执行超出了响应时限，或者飞书的 `streamUpdateCardContent` 慢网络下未正确 flush。
- **解决办法**：

  1. 检查桌面端仪表盘中的 "延迟" 及 "最新请求" 详情，确认后端接口是否仍在接收 SSE 流。
  2. 如果日志显示已完成，但卡片标题未变，直接发送新提问触发下一轮，新卡片会自动纠偏并修复旧卡片的状态。
  3. 若长时间无响应，请在桌面端点击 **"重启代理"**。

### 8.2 聊天中提示 "必需的 lark-\* skills 未安装完整"

- **原因分析**：首次自动配置时，桌面端内置环境在自动释放或初始化阶段被外部权限或磁盘锁所阻止。
- **解决办法**：

  1. 请确认应用对 `~/.config/lingma-proxy/` 及其配置文件夹有完整的读写权限。
  2. 在桌面端的“设置”页面中点击“保存配置”，应用会自动尝试重新释放和配置内置环境，无需手动通过命令行执行配置。

### 8.3 设备流授权登录时返回 91009 错误

- **原因分析**：飞书企业租户实施了组织级对外分享安全策略，管控了 API 授权。
- **解决办法**：请联系您的飞书租户管理员，在后台调整并开放组织级对外应用授权策略。

### 8.4 寻求协助与支持

如果您在安装、配置或使用该 Agent 的过程中遇到任何疑问或无法解决的异常，可以直接与我联系以协助处理。
