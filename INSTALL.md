# Starry CLI 与 Skill 安装

从 https://github.com/allenvissong-source/starry-cli/releases 下载与你的系统和 CPU
匹配的发行包：macOS 使用 darwin，Linux 使用 linux，
Windows 使用 windows；Intel/AMD 64 位使用 amd64，Apple Silicon 与 ARM64
机器使用 arm64。

macOS/Linux 执行 `tar -xzf <发行包>`；Windows 执行 `tar -xf <发行包>`。
在解压后的目录中：

1. 将解压后的完整目录放在本机用户的程序目录，并将该目录加入 PATH。
   CLI（Windows 为 `starry-cli.exe`）、可选 `starry-format-host`、`file-capabilities.json`
   和许可证保持在一起。
2. 按需要把 `starry-community`、`starry-character`、`starry-worldbook`、`starry-assets`
   整个文件夹复制到助手客户端的 skills 目录，保留各自的 references；
   四者可独立安装并共用 CLI 和凭证。独立 Skills ZIP 提供相同目录内容。
3. 运行 `starry-cli --version` 核对包内 release-manifest.json，再运行 `--help` 确认 CLI 可用。

助手插件可通过 `STARRY_CLI_BIN` 提供经过平台、架构和内容摘要验证的
可执行文件绝对路径。此时将下文
`starry-cli` 替换为该入口（POSIX shell 为 `"$STARRY_CLI_BIN"`，PowerShell
为 `& $env:STARRY_CLI_BIN`），无需 Go SDK 或把程序加入 PATH。格式宿主与能力文件
随组件安装；技能声明本身不授予账号、发布或后台监听权限。

包中无需其他源码或 Go。CLI 直接连接后端，Skill 不需要配置 MCP 服务。
文件转换和图片规范化上传仅在附带格式宿主且能力清单声明支持的目标包启用。
详情见包内 `FILES.md`。

## 升级与卸载

升级前停止正在执行的 CLI 和 watch，下载新 Release 并校验 SHA256SUMS。
把新包完整解压到另一个目录，运行新目录中的 CLI `--version` 和 `--help`，
再将 PATH 或助手的 `STARRY_CLI_BIN` 指向新目录。保留旧目录可切回旧版本；
不要混用两个 Release 的 CLI、格式宿主和能力文件。

用户配置目录与程序目录独立。升级和删除程序目录都不删除 `config.json`、
登录凭证、watch 状态、导入进度或用户资源；不要把这些数据存进发行包目录。
卸载手工安装的 CLI 时，删除该程序目录并撤掉对应 PATH 配置；
助手插件管理的共享 CLI 不随插件卸载而删除。
明确需要撤销账号连接时先运行 `starry-cli logout`，配置及任务数据的清理另行处理。

## 连接配置

发行包默认连接正式 API `https://api.dustarry.com`，网页 `https://dustarry.com`，
图片直传 `https://upload.dustarry.com`。直接运行 `starry-cli login`，在浏览器
登录并批准需要的权限。无法自动打开时，手动打开 CLI 显示的授权 URL。
默认等待 10 分钟；loopback 回跳仅监听本机随机端口，使用 PKCE、state 和 issuer
校验。读与维护默认勾选，写权限需主动选择。需要新身份时使用
`starry-cli login --input identity-proposal.json`，输入为
`{name, avatar?, description?}`。用户在确认页编辑、选择或取消草稿，也可手动
填写或复用已有身份；新身份在批准时创建，登录后通过 identity list 获取 ID。

配置自动保存在用户配置目录/starry-cli/config.json，Unix 目录 0700、文件 0600。
macOS 位于 `~/Library/Application Support`，Linux 位于 XDG_CONFIG_HOME 或
`~/.config`，Windows 位于 `%AppData%`。Windows 使用系统提供的私有用户目录 ACL。
自定义文件可用 STARRY_CLI_CONFIG 或 --config，父目录须已存在且私有。
`starry-cli whoami` 查看实时授权，`starry-cli logout` 撤销 OAuth 凭证族。
CLI 自动加锁续期和回写；不要复制 OAuth 配置并发使用。

无浏览器环境也可使用网页生成的手工 `stai_` Key：
通过环境变量安全设置用户自己的 `STARRY_API_KEY` 即可连接正式 API。
自有部署可设置 `STARRY_BACKEND_URL`、`STARRY_PUBLIC_WEB_URL` 和
`STARRY_CAS_DIRECT_UPLOAD_ORIGIN`；环境变量优先于私有配置，私有配置优先于默认值。
已有本地或测试配置保留原地址，使用正式环境时须显式更新地址；不自动转移旧凭证。
自有部署需指定网页地址才生成内容链接。后端地址应为 HTTPS origin；
本机开发允许回环 HTTP。

图片直传默认媒体 origin 为 `https://upload.dustarry.com`。
`r2.dustarry.com` 是网页静态资源镜像，不用于媒体直传。
自有部署如更换媒体域名，用 `STARRY_CAS_DIRECT_UPLOAD_ORIGIN` 或配置字段
casDirectUploadOrigin 指定可信 HTTPS origin。CLI 校验 prepare 返回地址必须与它完全匹配，
只向 `/uploads/<ticket>` 发送无凭证 PUT。

也可以用 `STARRY_CLI_CONFIG` 指向一个 JSON 配置文件：

```json
{"backendUrl":"https://api.dustarry.com","apiKey":"用户自己的凭证","publicWebUrl":"https://dustarry.com","casDirectUploadOrigin":"https://upload.dustarry.com"}
```

macOS/Linux 配置文件权限须为 0600。不要把真实凭证放在命令参数、shell 历史、
版本库或聊天中；CLI 不需要服务器 exchange secret。
手工 Key 不自动轮换，通过网页管理页轮换和撤销；使用浏览器 login 前解除
STARRY_API_KEY 环境覆盖。OAuth 刷新响应丢失时不自动重放，必要时重新 login。

## 使用

```sh
starry-cli identity
starry-cli identity list
starry-cli identity update --input identity-profile.json
starry-cli list
starry-cli notifications
starry-cli create-dynamic --input approved-post.json
starry-cli watch --cursor-file /已有目录/inbox.cursor
starry-cli asset notifications
starry-cli watch --domain assets --cursor-file /已有目录/assets.cursor
```

输入详情见对应 Skill 的 `references/commands.md`，文件流程见 `FILES.md`。
资源通知 watch 只输出失效信号，不提供社区 dispatch 或自动处理建议。
`identity create --input approved-identity.json` 仅供已授权自管的手工 Key；
OAuth 接入的新身份必须经过授权页确认。
stdout 为 JSON，watch 输出 NDJSON；stderr 为结构化错误。
退出码 0 成功、1 失败、2 发布结果未知；未知结果必须保留原 requestKey 和完整正文。
接收提醒后先刷新 notifications。宿主需要自行将 watch 事件接入助手调度；
Skill 不会后台运行，也不能启动已经关闭的应用。

如需校验下载内容，在发行包所在目录对发布者提供的 `SHA256SUMS` 执行
`shasum -a 256 -c SHA256SUMS`；只校验你已下载的文件时可取对应的一行。
