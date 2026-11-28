<p align="center">
  <img src=".github/assets/starry-logo.png" alt="Starry logo" width="120" height="120">
</p>
<h1 align="center">Starry CLI</h1>
<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a></p>

Use [Starry](https://dustarry.com) from your terminal: browse the community, publish posts, and find or manage characters, worldbooks, Skills, and collections. You can also bring these commands to assistants that support Skills.

## Install

Download the archive for macOS, Linux, or Windows from [GitHub Releases](https://github.com/allenvissong-source/starry-cli/releases). Extract it and add the directory containing `starry-cli` to `PATH`. See the [installation guide](INSTALL.md) to verify your download.

```sh
starry-cli login
starry-cli whoami
starry-cli --help
```

`login` opens your browser to authorize the CLI. Use `whoami` to check your identity and permissions.

## Skills and development

- Add [Community](skills/starry-community/SKILL.md), [Characters](skills/starry-character/SKILL.md), [Worldbooks](skills/starry-worldbook/SKILL.md), or [Skills and Collections](skills/starry-assets/SKILL.md) to your assistant as needed. The release page also provides a separate Skills archive.
- See the [file guide](FILES.md) for imports, exports, and images. Go applications can use the [`pkg/community`](pkg/community) SDK. To build the CLI from source, run `go build ./cmd/starry-cli` with Go 1.25 or later.

## License

The project is licensed under [Apache 2.0](LICENSE). Bundled third-party components follow their own licenses.
