# 世界书命令与输入

命令均带 `starry-cli`，使用 `--input FILE|-` 接收一个 JSON 对象，最多 64 KiB。

| CLI | MCP |
| --- | --- |
| asset search/get/package/revisions/lineage/diff | assets_search/get_revision/get_package/list_revisions/list_lineage/diff |
| worldbook list/get/create/update/delete | catalog_list_worldbooks/get_worldbook/create_worldbook/update_worldbook/delete_worldbook |
| worldbook entries list/get/create/update/delete/apply | catalog_list_worldbook_entries/get_worldbook_entry/create_worldbook_entry/update_worldbook_entry/delete_worldbook_entry/apply_worldbook_entry_changes |
| worldbook media set | catalog_set_worldbook_media |
| character bindings/bind/unbind | catalog_list_character_bindings/bind_worldbook/unbind_worldbook |
| asset preview/publish/policy/operation | assets_preview_publish/publish/update_policy/get_operation |
| asset fork | assets_fork |
| asset suggestions list/get/create/withdraw/reject/adopt | assets_list_suggestions/get_suggestion/create_suggestion/withdraw_suggestion/reject_suggestion/adopt_suggestion |
| asset comments list/create | assets_list_comments/create_comment |
| asset stats/notifications/inbox | assets_get_my_stats/list_notifications；starry://assets/inbox |
| media upload/operation | assets_prepare_upload/finalize_upload/get_upload |
| worldbook import/export、media download | 本机 CLI；远程 MCP 无本机文件入口 |

摘要需要 `assets.worldbook.read`，全文再需 `.package.read`；私有书需要 `catalog.worldbook.read`，写入再需 `.write`，删除根书 `.delete`，槽位 `.media`。上传字节 `media.upload`。发布 `.publish` 与独立 `assets.publish.as_owner|as_ai`；策略 `.policy.manage`；fork/建议/评论/通知各需对应 scope。角色绑定另需 `catalog.character.read` 与 `.bindings.write`。缺权限走正规增量授权。

## 私有书与条目

新书 create 指定新的规范 UUID、requestKey 与 fields，例如 name/description。
get 输入 `{"id":"书UUID"}`，响应 version 是根 entity version，readingVersion 包含条目与引用变化。

条目 list 输入 `{"bookId":"书UUID","limit":20}`；有 page.hasMore 时重复同一参数和 cursor。
get 输入 `{"bookId":"书UUID","entryId":"条目UUID","bodyLimit":6000}`；逐页读全部所需字段。

update 输入：

```json
{"id":"条目UUID","parentId":"书UUID","requestKey":"稳定意图","expectedVersion":2,"expectedReadingVersion":"5","fields":{"content":"更新后的内容","keys":["月港"]}}
```

create 指定新条目 UUID、parentId、父书 readingVersion、fields，不填旧 entity version；delete 需要条目当前 version 与父书 readingVersion。apply 输入 parentId、父 readingVersion、稳定 requestKey 和 1–50 个 changes，每项 action=create|update|delete、id、fields；已有项再加 expectedVersion。批次不可重复 ID。

规范书字段包括 name、description、token_budget、scan_depth、recursive_scanning 等当前 Catalog 字段；规范条目包括 entry_uid、keys、secondary_keys、content、comment、enabled、position、entry_order、probability 和各匹配、递归/分组字段。导入编解码器负责外部 uid/order 映射，不猜值；未知字段后端拒绝。position 覆盖 before_char、after_char、before_author_note、after_author_note、at_depth、before_example、after_example、outlet 的模型映射，不丢八种位置或外部数字 ID。

媒体设置输入 id、expectedVersion、expectedReadingVersion、mediaRole=avatar|cover|illustration|background、本人注册的 mediaAssetId；null 清所选槽。修改条目不会清四槽。

## 正式阅读、发布与文件

asset package 输入 type=worldbook、owner/slug、固定 revision、profile=full_package；
group=all 读书级配置，group=entries 分页读条目元数据，group=entry 加 entryOrdinal 读取所选条目正文。沿用首屏固定 revision 与全部原参数续读；entries 与普通列表最大 20，bodyLimit=1000–12000。asset revisions 只列一页修订摘要；asset lineage 返回当前节点、可见父节点及一页子节点，按子节点的 type/owner/slug 逐层展开。asset search 的元数据匹配结果按更新时间及 ID 降序排列，中文分词多词为 AND；游标失效应重新开始，不自行降级为空结果。

preview 输入 `{"type":"worldbook","draftId":"书UUID","slug":"目标slug","publishAs":"owner","requestKey":"稳定预览"}`。检查 result.snapshot 与依赖；publish 保留所有选择，加 previewId=预览 operationId、snapshot，使用独立稳定发布 requestKey。首次可选择 visibility/packageAccessPolicy，后续保留策略。策略调整带当前 policy_N ifMatch 与用户已批准的 confirmations，不自动补确认。

worldbook import 输入 file/progress/requestKey、inspectOnly=true，检查后原 key/progress 改 false 保存；不会发布。export 输入 source=workspace、id、output，或 source=published、owner/slug/revision、output。只有带格式宿主的发行包支持；输出路径不能已存在。独立 JSON 无法携带媒体则阻塞，不输出残缺书。

## 协作与通知

另一账号固定读取可派生的完整源版本，准备未发布的完整本人草稿。asset fork 输入 type=worldbook、源 owner/slug、当前 expectedRevision、requestKey；附着草稿再加 draftId、expectedDraftVersion、expectedReadingVersion。返回 fork.slug 与谱系，fork 不拷贝内容到草稿。编辑后发布到 fork.slug，保存真实 proposal repository ID/revision。

suggestions create 输入 type、原 owner/slug、当前 expectedRevision、稳定 requestKey、title/body、
proposalContentItemId、proposalRevisionId。只接受自己真实 fork；不能任意引用私有库。
get 输入 type/owner/slug/suggestionId；side=base|proposal 逐页读取两侧固定内容，只有作者与原资源 owner 可读。list 支持 status=open|withdrawn|rejected|adopted、limit/cursor。
withdraw/reject/adopt 输入同一地址、suggestionId、当前 expectedRevision、requestKey；adopt 再需 publishAs 与允许 AI（如选 ai）。过时 head 返回冲突，重新阅读与决定。

comments create 输入地址、当前 expectedRevision、requestKey、body、publishAs=owner；授权 comment.create 与 owner 签名，不需要发布资源。review 不等于公开评论。list 仅返回公开评论和本人的待审评论。

operation 输入 `{"action":"assets.worldbook.suggestion-adopt","requestKey":"原意图"}` 或 operationId。保留窗口 24 小时，原结果在 data.result，不用当前 head 重解释旧写意图。
stats 输入 type=worldbook，只提供本人真实计数，unavailable 不补零。
notifications 输入 type=worldbook|all、limit/cursor；混合只返回获准类型，不标记主账号已读。目标不可见只有 state=unavailable，不泄露地址。

`watch --domain assets --cursor-file /私有已有目录/assets.cursor`；与社区用不同文件。MCP 订阅 `starry://assets/inbox`，更新与重连后刷新通知，宿主保留跨轮事件 ID 去重。失效信号可以合并，不保证逐条队列，不自动回复或处理建议。
