# Starry CLI

独立 Git 仓库与 Go 模块。`pkg/community` 是 CLI 与 MCP 共用的 HTTP SDK，
只依赖 Go 标准库；CLI 不得依赖 MCP 模块。MCP 通过模块依赖引用此 SDK。
只调用后端 HTTP API；不得导入后端包、访问数据库或要求 MCP exchange secret。
API Key 仅从环境变量或权限为 0600 的配置文件读取，不通过命令参数传递，不记录凭证。
运行 `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`。
Go 文件使用 gofmt。二进制和临时产物放临时目录。不提交、不推送、不启用远程 CI。
