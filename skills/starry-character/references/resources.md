# Skill、合集与共享资产命令

目录：连接；私有编辑；绑定与成员；正式阅读；获取与更新；发布协作；文件与通知。

输入均为一个 JSON 对象，通过 `starry-cli COMMAND --input FILE|-` 传入，普通命令最大 64 KiB；Skill create/update 和角色 Skill bind/update/apply 的完整 JSON 意图最多 4 MiB。输出 `{"data":...}`；写结果通常在 `data.result`，同时返回 operationId。失败在 stderr；退出码 2 表示结果未知。

## 连接与身份

`--version` 输出发行号、源码基准提交、源码摘要、修改状态和目标平台。`whoami` 输出实时授权；`login` 浏览器 PKCE 授权，`logout` 撤销本连接 OAuth 凭证族。
`identity` 读取主账号；`identity list` 列本连接允许的身份，`identity update` 输入 id 和需要修改的 name/avatar/description。仅手工 Key 支持 `identity create`，输入 requestKey/name/description；OAuth 新身份在授权页审核创建。不自动授权。

## 私有 Skill 与合集

| CLI | 输入或用途 | MCP |
| --- | --- | --- |
| skill list / collection list | limit=1–20、cursor | catalog_list_skills / catalog_list_collections |
| skill get / collection get | id、bodyLimit、cursor，读完所有字段 | catalog_get_skill / catalog_get_collection |
| skill create/update/delete | 规范字段、版本和 requestKey | catalog_create_skill/update_skill/delete_skill |
| collection create/update/delete | 规范字段、版本和 requestKey | catalog_create_collection/update_collection/delete_collection |
| resource use-access / edit-access | type、id，服务端解析来源与依赖 | catalog_check_use_access / catalog_check_edit_access |

创建指定新的规范小写 UUID；更新/删除用返回的 entity version 和聚合 readingVersion：

```json
{"id":"12345678-1234-4234-8234-123456789abc","requestKey":"skill-edit-001","expectedVersion":3,"expectedReadingVersion":"7","fields":{"summary":"只修改摘要"}}
```

省略字段保留；只在字段支持时用 null 清除。Skill fields 使用 display_name、summary、scope、files 等规范字段。文件库存每项为 path、media_type、content（标准 Base64 字节），保留 SKILL.md 及 references/scripts/assets 的完整路径与内容，不能只保存主正文。scope 为后端允许值，如 character_bound。未知字段拒绝。标准目录导入优先 `skill import`，避免手工重建库存。合集 fields 用 name、description 等当前规范字段。

读取需 `catalog.skill.read` / `catalog.collection.read`；写入再需对应 `.write`；删除根资源需 `.delete`。完整正式阅读与草稿权限分别授予。

## 角色 Skill 绑定、合集成员

| CLI | 输入 |
| --- | --- |
| character skills list | characterId、limit=1–50、cursor |
| character skills bind | 新 id、parentId=角色UUID、expectedReadingVersion、requestKey、fields |
| character skills update / unbind | 绑定 id、parentId、绑定 expectedVersion、角色 expectedReadingVersion、requestKey |
| character skills apply | parentId、expectedReadingVersion、requestKey、1–50 个 changes |
| collection items list | collectionId、limit=1–50、cursor |
| collection items apply | parentId=合集UUID、expectedReadingVersion、requestKey、1–50 个 changes |

MCP 对应 catalog_list_character_skills、catalog_create/update/delete_skill_binding、catalog_apply_character_skills、catalog_list_collection_items、catalog_apply_collection_items。
绑定需 `catalog.character.bindings.write`，成员写需 `catalog.collection.write`；相关资源读取仍单独授权。

changes 每项 action=create|update|delete、id，create/update 再给 fields，已有项给 expectedVersion；整批原子提交且 ID 不重复。从读取的规范 fields 修改，不猜冻结 skill_snapshot、usage_overrides 或引用库存。解绑只删绑定。

绑定配置保持 skill_id、enabled、activation、required、purpose 和后端返回的稀疏使用者覆盖。Skill 新版采用下节的正式更新流程；不改写服务端授权来源。合集成员保持 target_type/target_id、顺序、分组和说明；保留关闭绑定与共享资源。

## 正式阅读与历史

| CLI | JSON 输入 |
| --- | --- |
| asset search | type=character/worldbook/skill/collection/all、keyword、tags、author、limit=1–20、cursor |
| asset get | type、owner、slug、可选 revision |
| asset package | 同一地址、profile=basic_package/full_package、group、bodyLimit=1000–12000、cursor |
| asset revisions | 同一地址、limit=1–20、cursor |
| asset lineage | type、owner、slug、limit=1–20、cursor |
| asset diff | type、owner、slug、to=修订UUID、可选 from、bodyLimit、cursor |

MCP 对应 assets_search/get_revision/get_package/list_revisions/list_lineage/diff。
正式摘要需 `assets.<type>.read`，完整包/历史全文还需 `.package.read`。首请求解析的 revision 固定用于后续分页和依赖。group=entries 列条目元数据，group=entry 再给 entryOrdinal；group=mediaFiles 列文件，group=delivery 再给 fileRefId。group=all 仍需读完字段分页；不要把截断 body 当完整文件库存。搜索按更新时间和 ID 降序，中文分词多词 AND；保留返回顺序，游标失效重新首屏。

## 获取与已获取 Skill 更新

`asset acquire-plan` / MCP assets_plan_acquisition：

```json
{"type":"skill","owner":"1000","slug":"behavior","revision":"12345678-1234-4234-8234-123456789abc","mode":"editable","strategy":"reuse"}
```

mode=read_only|editable，strategy=reuse|copy。selection 是根直接成员的准确 `type/owner/slug/revision` 地址数组；省略选全部，空数组选无成员，选中成员仍保留全部依赖。审读 plan_hash、新建、复用、冲突与 binding_updates。

`asset acquire` / MCP assets_acquire 将相同 revision 改名为 expectedRevision，加独立稳定 requestKey 和 planHash=返回的 plan_hash，保留全部模式/选择/策略。plan_changed 重新审读；结果未知查 `asset operation` action=`assets.<type>.acquire`。回读返回 resource_ids、reused_ids。复用只针对未修改同版资源，其他版本和本地修改保留。

`asset update-source` / MCP assets_get_skill_update_source 输入 `{"id":"已有获取SkillUUID"}`，需 catalog.skill.read 与 assets.skill.read，从受保护谱系取得原获取 revision 与最新可读版本。比较两个固定包后，计划和提交均传：

```json
{"sourceSkillId":"旧SkillUUID","expectedVersion":4,"bindings":[{"id":"用户选定绑定UUID","expectedVersion":2}]}
```

以上作为 `skillUpdate` 字段。bindings 默认不选；显式空数组只获取新版。额外需 catalog.character.bindings.write。提交原子获取新版、替换选定绑定锚点，旧 Skill/其他角色保留；作者推荐随新版，用户覆盖保留。回读 updated_bindings 的旧/新 ID 和配置。

## 发布、策略与协作

`asset preview` 输入 type/draftId/slug/publishAs/requestKey；ai 再给已批准 aiIdentityId。首次可选 visibility 与 packageAccessPolicy。检查 `data.result.snapshot`；`asset publish` 保留全部选择，加 previewId=预览 operationId、snapshot 和独立发布 requestKey。后续普通发布保留既有策略。发布需 assets.<type>.publish 和独立 assets.publish.as_owner|as_ai。

`asset policy` 输入 type/slug/requestKey/ifMatch=当前 policy_N、patch={visibility?,derivative_policy?,package_access_policy?,confirmations?}。confirmations 仅取用户明确批准项，需 `.policy.manage`。

`asset fork` 输入 type/owner/slug/expectedRevision/requestKey；可附着本人准备好的未发布完整 draftId、expectedDraftVersion、expectedReadingVersion。返回 fork.slug/谱系；不自动拷贝草稿。linkedAssets 用实际 linked_assets ID 和 mode=reference|fork|omit，fork 模式当前保留固定引用，不声称复制依赖。

`asset suggestions list/get/create/withdraw/reject/adopt`：list 地址+status/limit/cursor；get 地址+suggestionId、可选 side=base|proposal/bodyLimit/cursor，逐页读双方固定包。create 地址+当前 expectedRevision/requestKey/title/body/proposalContentItemId/proposalRevisionId，只接受自己真实 fork 的已发布固定版本。withdraw/reject/adopt 地址+suggestionId/expectedRevision/requestKey；adopt 再给 publishAs/允许 AI。过时 head 重新比较。权限 `.fork`、`.suggestion.create|manage` 分别授予，采纳另需 publish 与署名。

`asset comments list/create`：list 地址+limit/cursor；create 地址+expectedRevision/requestKey/body/publishAs=owner，需 `.comment.create` 和 owner 署名，不需资源 publish。review 表示待审。MCP 均为 assets_ 对应工具。

`asset operation` 传 operationId，或 action+原 requestKey；24 小时保留窗口，未找到不能自动重做。

## 文件、媒体与通知

详见同目录 [完整工作流](workflows.md)。`skill import/export`、`collection import/export` 使用文件宿主。`media upload` 使用相同宿主先规范化图片再 SHA256/直传；没有宿主的包不支持图片处理。`media download` 和所有结构化 HTTP 命令不依赖宿主。

`asset stats` 输入 type，仅本人真实统计；`asset notifications` 输入 type=任一类型|all、limit/cursor；`asset inbox` 当前失效版本。需对应 `.notifications.read`，unavailable 不补零、不泄露目标。
`watch --domain assets --cursor-file /已有私有目录/assets.cursor` 输出 NDJSON；事件和重连刷新通知。不标记主账号已读，不提供社区 dispatch 或自动处理建议。MCP 订阅 `starry://assets/inbox`。
