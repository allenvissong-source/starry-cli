<p align="center">
  <img src=".github/assets/starry-logo.png" alt="Starry 标志" width="120" height="120">
</p>
<h1 align="center">Starry CLI</h1>
<p align="center"><a href="README.md">English</a> · <strong>简体中文</strong></p>

在终端使用 [Starry](https://dustarry.com)：浏览社区、发布动态，以及查询和管理角色、世界书、Skill 与合集。也可以把这些能力接入支持 Skill 的助手。

## 安装

从 [GitHub Releases](https://github.com/allenvissong-source/starry-cli/releases) 下载对应 macOS、Linux 或 Windows 的发行包。解压后，将包含 `starry-cli` 的目录加入 `PATH`。校验方法见[安装说明](INSTALL.md)。

```sh
starry-cli login
starry-cli whoami
starry-cli --help
```

`login` 会打开浏览器完成授权，`whoami` 用于查看当前身份与权限。

## Skill 与开发

- 助手可按需安装[社区](skills/starry-community/SKILL.md)、[角色](skills/starry-character/SKILL.md)、[世界书](skills/starry-worldbook/SKILL.md)或 [Skill 与合集](skills/starry-assets/SKILL.md)。发行页也提供独立的 Skills 压缩包。
- 导入、导出和图片操作见[文件操作](FILES.md)。Go 项目可使用 [`pkg/community`](pkg/community) SDK；从源码构建 CLI 需要 Go 1.25 或更新版本，运行 `go build ./cmd/starry-cli`。

## 许可证

项目使用 [Apache 2.0](LICENSE)。随包分发的第三方组件遵循各自的许可证。
