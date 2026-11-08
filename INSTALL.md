# Starry CLI 与 Skill 安装

选择与你的系统和 CPU 匹配的发行包：macOS 使用 darwin，Linux 使用 linux，
Windows 使用 windows；Intel/AMD 64 位使用 amd64，Apple Silicon 与 ARM64
机器使用 arm64。

macOS/Linux 执行 `tar -xzf <发行包>`；Windows 执行 `tar -xf <发行包>`。
在解压后的目录中：

1. 将 `starry-cli`（Windows 为 `starry-cli.exe`）放到 PATH 中的可执行目录；包内若有 `starry-format-host`，与 CLI、`file-capabilities.json` 一起放在同一目录。

Starry 随包环境通过 `STARRY_CLI_BIN` 提供经过平台、架构和内容摘要验证的
可执行文件绝对路径，并通过 `STARRY_CLI_CONFIG` 指向实例私有配置。此时将下文
`starry-cli` 替换为该入口（POSIX shell 为 `"$STARRY_CLI_BIN"`，PowerShell
为 `& $env:STARRY_CLI_BIN`），无需 Go SDK 或把程序加入 PATH。格式宿主与能力文件
随组件安装；技能声明本身不授予账号、发布或后台监听权限。
2. 按需要把 `starry-community`、`starry-character`、`starry-worldbook` 整个文件夹复制到助手客户端的 skills 目录，保留各自的 references；三者可独立安装并共用 CLI 和凭证。
3. 运行 `starry-cli --help` 确认 CLI 可用。

包中无需其他源码或 Go。CLI 直接连接后端，Skill 不需要配置 MCP 服务。
文件转换仅在附带格式宿主的目标包启用；macOS ARM64 已验证，其他目标等待
原生宿主验证。详情见包内 `FILES.md`。

## 连接配置

通过环境变量安全设置 `STARRY_BACKEND_URL`，运行 `starry-cli login`，在浏览器
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
通过环境变量安全设置 `STARRY_BACKEND_URL` 与用户自己的 `STARRY_API_KEY`。
可选 `STARRY_PUBLIC_WEB_URL` 用于生成内容链接。后端地址应为 HTTPS origin；
本机开发允许回环 HTTP。

也可以用 `STARRY_CLI_CONFIG` 指向一个 JSON 配置文件：

```json
{"backendUrl":"https://你的后端域名","apiKey":"用户自己的凭证","publicWebUrl":"https://你的站点"}
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
