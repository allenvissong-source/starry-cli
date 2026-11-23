---
name: starry-assets
description: Use Starry CLI for Skill and collection CRUD, character Skill bindings, fixed asset reading, acquisition and selected binding updates, publication, policies, collaboration, file transfer and resource notifications. Invoke for Starry 技能库、合集、获取资源、更新角色技能、资源发布 or 资源协作.
---

# Starry Assets

使用 `starry-cli` 的正式 HTTP 接口管理四类资源。先读 [命令与输入](references/commands.md)，再按任务读 [完整工作流](references/workflows.md)。无需 MCP；其他三个 Starry Skill 不必安装。

宿主提供 `STARRY_CLI_BIN` 时用该绝对入口替代 `starry-cli`；POSIX 为 `"$STARRY_CLI_BIN"`，PowerShell 为 `& $env:STARRY_CLI_BIN`。配置用 `STARRY_CLI_CONFIG`。独立安装从匹配平台发行包安装 CLI，将本 Skill 整个目录放到客户端 skills 目录，保留 references。文件与图片处理还需包内相邻的格式宿主；核对 `file-capabilities.json`。

执行 `starry-cli --version` 和 `starry-cli whoami` 核实版本、连接、实时权限。发行包默认 API `https://api.dustarry.com`、网页 `https://dustarry.com`、直传 `https://upload.dustarry.com`。未连接时执行 `starry-cli login`，由用户在浏览器完成登录和授权；自有部署才覆盖环境变量，已有测试配置须显式更新地址。不自行批准权限或抄写令牌。可用 `login --input identity-proposal.json` 提交 `{name,avatar?,description?}` 供用户审核；登录后 `identity list` 读取获准身份。退出用 `logout`。凭证仅环境变量或私有配置，Unix 配置文件 0600、目录 0700。

正式 owner 使用规范十进制主账号 UID 字符串；草稿、绑定、修订与文件引用各使用返回的 UUID。禁止把显示名称、媒体 hash 或草稿 ID 当作正式资源地址。

先读当前对象及全部所需分页，再形成修改。保存/另存前 `resource edit-access`；使用前 `resource use-access`。只读获取不授予编辑或再发布。源策略收紧时重新检查依赖，禁止用导出/另存绕过限制。

每个写意图使用稳定 requestKey，保持完整输入。版本冲突先重新读取并判断。结果未知时查询 `asset operation`（媒体用 `media operation`），保留原 key；不存在或过期不证明未提交。草稿写入、正式发布和通知处理分别回读确认；待审核不能声称公开。

只更新用户选定的角色 Skill 绑定。先从受保护谱系解析真实来源，再固定比较新旧包，审读计划、使用相同 planHash 提交，回读 updated_bindings 和旧 Skill。保留用户覆盖配置，不把全部角色自动升级。

明确发布身份 owner 或获准 AI。preview 与 publish 保持全部内容选择，发布引用返回 previewId/snapshot。策略 confirmations 必须来源于用户明确选择。建议采纳同时检查 manage、publish 和署名权限。

监听 `watch --domain assets`，与社区使用不同 cursor 文件；事件和重连后刷新资源通知。不自动回复、采纳建议或启动常驻任务。报告实际完成、待审核、受限及不可用状态。
