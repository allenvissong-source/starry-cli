# 角色、世界书、Skill 与合集文件操作

CLI 通过获准的 HTTP 接口操作工作区与正式资源。文件转换由同目录的
`starry-format-host`（Windows 为 `.exe`）执行，不访问 App 数据库。
普通输入 JSON 最多 64 KiB；Skill 创建/更新与绑定创建/更新/批量 apply
携带完整库存时，完整 JSON 意图最多 4 MiB，与后端一致。文件路径只传给本机 CLI。远程 MCP 接收 SHA256
上传句柄，不访问用户文件系统。

## 格式与发行

正式资源获取先运行 `asset acquire-plan`（MCP `assets_plan_acquisition`），核对账号库内的新建、同版复用与另存冲突，再把返回的 `plan_hash` 作为 `planHash` 传给 `asset acquire`。两步保持相同固定 revision、模式、选择和策略；`plan_changed` 要重新审读，结果不明则保留原提交输入查操作，不自动生成新意图。

`selection` 为根成员地址 `type/owner/slug/revision` 数组，省略选择完整图，空数组选择无成员。所选依赖完整保留。`strategy` 为 `reuse`（默认）或 `copy`；复用只针对未改动的同模式同版资源，其他资源保留并另存。返回 `resource_ids` 与 `reused_ids` 供完整回读。普通创建、更新和复制不能提供或改写内部 `catalog_acquisition_*` 授权库存。

已获取 Skill 的新版先用 `asset update-source`（MCP `assets_get_skill_update_source`）传已有 `id`，从受保护谱系取得真实来源、原获取 revision 与最新可读 revision。读取两个固定包完成比较；不要从可编辑来源标签猜地址。读取需 `catalog.skill.read` 与 `assets.skill.read`。

更新选定角色时，计划和提交都传相同的 `skillUpdate: {sourceSkillId, expectedVersion, bindings: [{id, expectedVersion}]}`。版本是 Catalog `entity_version`，绑定默认不选，显式空数组只获取新版。计划 `binding_updates` 可回读作者推荐和保留的稀疏使用者配置；提交原子获取新版并替换选定绑定锚点，旧 Skill 与其他角色保留。回执 `updated_bindings` 含旧/新绑定 ID；完整回读这些资源以及原绑定删除。此操作额外要求 `catalog.character.bindings.write`，计划过期重新审阅，结果不明保留原输入查原操作。

角色支持 JSON、PNG `chara`、CharX 和当前 codec 的 `.starry` 角色包。
独立世界书只支持 JSON；`.starry` 角色包可携带多本世界书。
角色、Skill 与合集的 `.starry` 包统一读写整数 `package_version=1`，由共享编解码层验证完整资源、Skill 文件库存、绑定、作者归属及获取来源。角色包必须声明 Skill 库存和绑定，未附带 Skill 时均为空。其他格式号和不完整结构明确拒绝。`character_version` 是作者维护的内容版本，原样保留。
Skill 支持标准 Markdown、单 Skill 目录 ZIP、独立 `.starry`；
合集支持 `.starry`，保留角色、世界书、共享 Skill、关闭的 Skill 绑定、
成员排序/分组/说明及获取来源。带 `.starry` manifest 的 ZIP 走严格包解析，
原 Skill ID 与获取来源保持一致。
默认展示预设接受角色名或制作工具生成的 `Default`；自定义预设名称、说明及
归档状态仍拒绝，避免把有意义的展示配置静默丢弃。
角色 JSON、PNG、CharX 中显式附带的世界书即使没有条目，也保留书配置与
embedded 绑定；空条目不会被当作未附带世界书。

只有带 `file-capabilities.json` 中 `fileImportExport:true` 及格式宿主的包
支持文件转换。图片规范化上传也需要该宿主；
`imagePreparation` 与 `mediaUpload` 在能力清单明确标识。下载、结构化草稿编辑与读取不依赖
格式宿主。用户不需 Go、Dart、Flutter SDK 或后端源码。

维护者通过 `STARRY_FORMAT_HOST_DIR` 提供与目标系统和架构一致的预编译宿主。
目录结构为 `<目录>/<系统>-<架构>/starry-format-host`，Windows 程序增加 `.exe`。
每个宿主旁须提供 `<程序名>.NOTICES`，包含实际编译组件的第三方许可证，
发行包保留为 `FORMAT_HOST_NOTICES`。
打包仅消费明确提供的程序，未提供的目标在能力清单中关闭对应功能：

```sh
STARRY_FORMAT_HOST_DIR=/已有的宿主目录 sh tools/package-release.sh /已有空目录
```

所有包均生成 SHA256SUMS。
升级时先核验校验值，再把 CLI、宿主、能力文件一起替换。没有配置发布者
签名或公证，校验和只能验证字节，不能证明发布者身份。

## 导入

先准备一个私有目录，写输入 JSON：

```json
{"file":"/用户指定/card.starry","progress":"/私有目录/import.json","requestKey":"stable-file-intent","inspectOnly":true}
```

```sh
starry-cli character import --input inspect.json
```

返回解析摘要、书和媒体数量、步骤数与警告；不进行业务写入。
随后将同一输入的 `inspectOnly` 改为 false，仍用同一 requestKey 与 progress：

```sh
starry-cli character import --input import.json
starry-cli worldbook import --input book-import.json
starry-cli skill import --input skill-import.json
starry-cli collection import --input collection-import.json
```

整个导入是显式多步保存，不是跨文件的大事务。所有支持的字段与依赖先解析，
为新对象生成稳定 UUID，逐步取得当前版本、写入私有草稿。四槽图片单独上传、
注册再绑定；不会发布。进度文件 0600、源文件副本和媒体暂存在 0700 的
`<progress>.files` 目录，保留外部数字 ID、UID 映射和完整原始文件。
第三方角色扩展不会成为可信身份、审核或 Catalog 字段。

中断后重新执行同一输入。媒体资产尚未开始登记时，可继续探测、上传或确认回执；
已开始登记的媒体和已尝试的草稿写入，先查询原操作，无法确认时停止。找不到
结果不代表未提交。不自动改 key、重放结果未知的写入、清空进度或重复建卡。
媒体注册可用 `media operation`；草稿与发布用 `asset operation`。
若进程被强制终止遗留 `<progress>.lock` 目录，先确认旧进程已退出再移除
该锁目录，保留进度和 source。部分保存失败时，可在 App 中继续处理已保存
对象；需要清理时依据进度逐个确认，不强制级联删书。

不同后端、账号或连接的进度不可互用。操作结果有保留窗口；过期或未知
结果不会变成自动重新导入。成功后本地源副本仍供核查，不自动删除。

## 导出

工作区输入：

```json
{"source":"workspace","id":"当前草稿UUID","output":"/已有目录/card.starry"}
```

正式版本输入：

```json
{"source":"published","owner":"资源所有者","slug":"资源slug","revision":"固定版本UUID","output":"/已有目录/card.starry"}
```

```sh
starry-cli character export --input export.json
starry-cli worldbook export --input book-export.json
starry-cli skill export --input skill-export.json
starry-cli collection export --input collection-export.json
```

省略正式 revision 时首请求解析为固定 head，后续正文、条目、书与媒体始终
绑定该版本。读取依赖重新检查权限；工作区最终重检所有聚合版本。
完整成功后才把临时文件落成到输出路径，不覆盖已有输出，不留下残缺包。

`.starry` 保留两种绑定、多书和四槽媒体。JSON/PNG/CharX 无法容纳整个图时
拒绝导出，例如多本书、reference 绑定、非头像媒体；独立世界书 JSON
不能带媒体。缺失/受限依赖、文件、Live2D/其他展示映射、一个槽位多图及
被关闭的世界书或表现关系也会阻塞，不能把被过滤的内容默认为空。
关闭的 Skill 绑定完整保留，Skill/合集导出使用 `.starry`。
作者元数据和不能由四槽 API 表达的表现预设（非默认、归档、独立描述/名称）
明确拒绝，使用产品完整导入器处理。CLI 不声称覆盖整个表现图。
工作区及正式合集递归获取依赖；不可读成员、文件计数不符、Live2D 成员等
导致整个导出失败，不静默省略。
`.starry` 来源提示在资源 ID 重映射后传给根 create 操作，服务端核验真实
同账号工作资源的谱系；其他账号的提示拒绝，须重新获取公开原件。
未知第三方角色扩展保存在导入的原始源副本，规范导出不声称逐字保留这些扩展。
仍可用原始副本恢复外部格式数据。

## 媒体

```json
{"file":"/用户指定/avatar.png","requestKey":"stable-media-intent","purpose":"avatar"}
```

`media upload` 支持 PNG/JPEG/GIF/WebP，原图最多 100 MiB，返回本人私有
媒体 UUID。内容 hash、媒体 UUID、正式 fileRefId 不可混用。注册未知时查询：

```json
{"sha256":"规范化后SHA256","mediaType":"image/webp","byteSize":12345,"requestKey":"原媒体key"}
```

`media operation` 只读取同一连接的原注册结果。MCP 对应
`assets_prepare_upload`、`assets_finalize_upload`、`assets_get_upload`。
本机 CLI 复用 App 的 ImageMediaIntake 与同版编码器，先规范化再计算 SHA256；
purpose=avatar|cover|background|content|illustration，默认 content。静态图转 WebP，
动画按统一编解码规则保留帧、透明度与时序；合规且带当前标记的图复用字节。
导入按实际槽位中的最大用途处理共享图片，同时更新图引用和进度摘要，
后续注册/恢复始终用规范化后摘要。发布与同步复用已入库字节。

直传顺序为：查已存在 blob → POST prepare → 无凭证 PUT 到配置媒体 origin 的
`/uploads/<ticket>` → 核对摘要/大小/receipt → POST complete → 注册资产。
正式直传 origin 为 `https://upload.dustarry.com`，自有部署可通过
STARRY_CAS_DIRECT_UPLOAD_ORIGIN 指定；禁止任意域名和跳转。
MCP 宿主先按同一图片入口准备 bytes，调用 prepare；present=true 时直接 finalize，
否则使用返回 uploadUrl/method/ticket 上传，核对 receipt 后将 ticket/receipt
连同同一 SHA256/mediaType/byteSize/requestKey 传入 finalize。远程工具不读取本机路径。

`media download` 必须使用工作区的 id/mediaRole 或正式版本的
owner/slug/revision/fileRefId。交付 URL 由权限端点解析；下载不跟随重定向，
不携带 Bearer 到媒体 URL，校验大小、类型和 SHA256 后落盘。
