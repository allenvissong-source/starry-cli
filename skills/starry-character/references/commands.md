# 角色卡命令

每次输入一个 JSON 对象，通过 `--input FILE|-`；最多 64 KiB。命令均带 `starry-cli` 前缀。MCP 用同名业务工具，不能访问本机路径。

| CLI | MCP |
| --- | --- |
| asset search/get/package/revisions/lineage/diff | assets_search/get_revision/get_package/list_revisions/list_lineage/diff |
| character list/get | catalog_list_characters/get_character |
| character create/update/delete | catalog_create_character/update_character/delete_character |
| character bindings/bind/unbind | catalog_list_character_bindings/bind_worldbook/unbind_worldbook |
| character media set | catalog_set_character_media |
| media upload/operation | assets_prepare_upload/finalize_upload/get_upload |
| asset preview/publish/policy/operation | assets_preview_publish/publish/update_policy/get_operation |
| asset fork | assets_fork |
| asset suggestions list/get/create/withdraw/reject/adopt | assets_list_suggestions/get_suggestion/create_suggestion/withdraw_suggestion/reject_suggestion/adopt_suggestion |
| asset comments list/create | assets_list_comments/create_comment |
| asset stats/notifications/inbox | assets_get_my_stats/list_notifications；starry://assets/inbox |
| character import/export、media download | 本机 CLI；远程 MCP 无文件系统入口 |

只写权限不隐含发布。正式摘要需要 `assets.character.read`；完整包再需 `.package.read`；私有草稿需要 `catalog.character.read`，写入再需 `.write`；绑定 `.bindings.write`；槽位 `.media`；字节上传 `media.upload`。发布 `.publish` 加 `assets.publish.as_owner|as_ai`；策略 `.policy.manage`；协作 `.fork`、`.suggestion.create|manage`、`.comment.create`；通知 `.notifications.read`。具体动作还检查当前对象权限；另一类型同理。

## 最小私有修改

`character get` 输入 `{"id":"草稿UUID"}`，读取返回的 version/readingVersion。

`character update` 输入：

```json
{"id":"草稿UUID","requestKey":"稳定修改意图","expectedVersion":3,"expectedReadingVersion":"7","fields":{"description":"本轮修改","first_message":"开场白"}}
```

创建指定新的规范 UUID 与 `fields`，不提交 expectedVersion。常用规范字段：
name、nickname、description、personality、scenario、first_message、alternate_greetings、
example_messages、system_prompt、post_history_instructions、depth_prompt、talkativeness、
creator_notes、character_version、tags。后端拒绝未知字段和可信署名字段。

绑定创建需新 id、parentId=角色草稿 UUID、角色 readingVersion，以及
`fields={worldbook_id,reference_type:"reference"|"embedded"}`。
解绑需绑定 id、parentId、绑定 version 与角色 readingVersion，不删除书。
媒体槽位需 id、两种当前版本、mediaRole 和本人注册的 mediaAssetId；null 只清除所选槽位。

## 分段读取与发布

正式 package 输入 `{"type":"character","owner":"所有者","slug":"slug","profile":"full_package","group":"all","bodyLimit":6000}`。后续页带首轮固定 revision 和原参数、cursor；结束以 page.hasMore=false 为准。历史/diff 可能含已删全文，需要相同完整包边界。

预览输入 `{"type":"character","draftId":"草稿UUID","slug":"目标slug","publishAs":"owner","requestKey":"稳定预览意图"}`。首次发布可明确 visibility/packageAccessPolicy；已发布资源默认保留策略。提交保留全部选择，requestKey 使用独立发布意图，并加 previewId=预览 operationId、snapshot=预览 result.snapshot。

`asset operation` 输入 `{"action":"character.update","requestKey":"原意图"}`；协作 action 例如 `assets.character.suggestion-adopt`。记录窗口 24 小时，过期不能自动重做。HTTP 写入结果在 `data.result`，操作号为 `data.operationId`。

## 两账号协作

1. 原作者开放的完整源版本可导出时，先固定读取/导出；另一账号导入完整私有草稿，核对字段、媒体和绑定。不能直接读原作者工作区。
2. `asset fork` 输入 type/owner/slug、expectedRevision=当前源 head、requestKey。要继续编辑，加本账号准备好的未发布 draftId、expectedDraftVersion、expectedReadingVersion。返回 `result.fork` 和 draftId；未附着草稿时返回正式 fork 结果，不自动建草稿。LinkedAssets 选择为 `[{id:"源 linked_assets 的 ID",mode:"reference|fork|omit"}]`；当前 fork 模式保留固定引用，不能声称已复制依赖。
3. 编辑附着草稿，preview/publish 到返回 fork.slug，保存 proposal repository ID 与 revision。必须遵守派生的可见性限制。
4. `asset suggestions create` 输入 type、原 owner/slug、当前 expectedRevision、稳定 requestKey、title/body、proposalContentItemId、proposalRevisionId。只接受自己真实 fork 的固定版本。
5. `suggestions get` 输入 type/owner/slug/suggestionId；加 side=base 或 proposal 获取两侧固定内容，逐页读完再比较。普通访客看不到私有建议。list 支持 status、limit（1–20）与 cursor。
6. withdraw/reject/adopt 使用 type/owner/slug、suggestionId、当前 expectedRevision、稳定 requestKey；adopt 再明确 publishAs 与允许的 AI（如选 ai）。拒绝只需 manage；采纳另需 publish 和签名。过时源 head 返回冲突。

评论 create 使用 type/owner/slug、当前 expectedRevision、requestKey、body、publishAs=owner；单独授予 comment.create 与 owner 签名即可，不需要资源 publish。评论状态可能是 review，不能声称已经公开。

stats 输入 `{"type":"character"}`，只统计本人资源/版本/已发布评论/开放建议；unavailable 指标不能补零或估算。notifications 支持 type=character|all、limit/cursor；仅授权类型可见，目标失去访问权时 state=unavailable。

`watch --domain assets --cursor-file /私有已有目录/assets.cursor` 输出合并失效通知；和社区用不同 cursor 文件。订阅资源为 `starry://assets/inbox`；事件后重新请求 notifications，重连也要刷新。Skill 不提供常驻任务或自动评论。
