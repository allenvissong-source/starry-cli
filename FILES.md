# 角色卡与世界书文件操作

CLI 通过获准的 HTTP 接口操作工作区与正式资源。文件转换由同目录的
`starry-format-host`（Windows 为 `.exe`）执行，不访问 App 数据库。
输入 JSON 最多 64 KiB；文件路径只传给本机 CLI。远程 MCP 接收 SHA256
上传句柄，不访问用户文件系统。

## 格式与发行

角色支持 JSON、PNG `chara`、CharX 和当前 codec 的 `.starry` 角色包。
独立世界书只支持 JSON；`.starry` 角色包可携带多本世界书。现有交换格式的
`package_version=2` 保持不变，接入功能对外统一称 V1。
角色 JSON、PNG、CharX 中显式附带的世界书即使没有条目，也保留书配置与
embedded 绑定；空条目不会被当作未附带世界书。

只有带 `file-capabilities.json` 中 `fileImportExport:true` 及格式宿主的包
支持文件转换。本机 macOS ARM64 已实际构建并验证；其余五个目标只交付 Go
CLI，等待各平台构建并验证宿主。下载、媒体上传、结构化草稿编辑与读取不依赖
格式宿主。用户不需 Go、Dart、Flutter SDK 或后端源码。

维护者用已有兄弟 codec 的 package_config 构建原生宿主：

```sh
sh tools/build-format-host.sh /已有目录/starry-format-host
python3 tools/test-format-host.py /已有目录/starry-format-host
sh tools/package-release.sh /已有空目录
```

打包会构建本机宿主；可用 `STARRY_FORMAT_HOST_DIR` 指向已验证的其他平台组件，
目录结构为 `darwin-arm64/starry-format-host` 等。所有包均生成 SHA256SUMS。
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
```

整个导入是显式多步保存，不是跨文件的大事务。所有支持的字段与依赖先解析，
为新对象生成稳定 UUID，逐步取得当前版本、写入私有草稿。四槽图片单独上传、
注册再绑定；不会发布。进度文件 0600、源文件副本和媒体暂存在 0700 的
`<progress>.files` 目录，保留外部数字 ID、UID 映射和完整原始文件。
第三方角色扩展不会成为可信身份、审核或 Catalog 字段。

中断后重新执行同一输入，先查询已尝试的原操作；无法确认时停止，找不到
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
```

省略正式 revision 时首请求解析为固定 head，后续正文、条目、书与媒体始终
绑定该版本。读取依赖重新检查权限；工作区最终重检所有聚合版本。
完整成功后才把临时文件落成到输出路径，不覆盖已有输出，不留下残缺包。

`.starry` 保留两种绑定、多书和四槽媒体。JSON/PNG/CharX 无法容纳整个图时
拒绝导出，例如多本书、reference 绑定、非头像媒体；独立世界书 JSON
不能带媒体。缺失/受限依赖、文件、Live2D/其他展示映射、一个槽位多图及
被关闭的绑定也会阻塞，不能把被过滤的内容默认为空。
未知第三方角色扩展保存在导入的原始源副本，规范导出不声称逐字保留这些扩展。
仍可用原始副本恢复外部格式数据。

## 媒体

```json
{"file":"/用户指定/avatar.png","requestKey":"stable-media-intent"}
```

`media upload` 支持 PNG/JPEG/GIF/WebP，单文件最多 128 MiB，返回本人私有
媒体 UUID。内容 hash、媒体 UUID、正式 fileRefId 不可混用。注册未知时查询：

```json
{"sha256":"原SHA256","mediaType":"image/png","byteSize":12345,"requestKey":"原媒体key"}
```

`media operation` 只读取同一连接的原注册结果。MCP 对应
`assets_prepare_upload`、`assets_finalize_upload`、`assets_get_upload`。
宿主向 prepare 返回的相对 PUT 路径上传字节；工具不提供任意 URL 抓取器。

`media download` 必须使用工作区的 id/mediaRole 或正式版本的
owner/slug/revision/fileRefId。交付 URL 由权限端点解析；下载不跟随重定向，
不携带 Bearer 到媒体 URL，校验大小、类型和 SHA256 后落盘。
