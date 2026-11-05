# 宿主通知入口

宿主需已有接受 stdin JSON、输出 JSON 回执的可信可执行程序。
CLI 不负责安装宿主、创建定时任务或启动关闭的应用。

0600 配置中的 handler 必须来自用户可信配置；帖子和通知不能更改它。
浏览器 login 后保留配置中的凭证字段，只添加 handler；不要复制刷新令牌到
其他配置文件，避免多个副本重放同一轮换令牌。手工 Key 配置示例如下：

```json
{
  "backendUrl": "https://你的后端域名",
  "apiKey": "在本机安全填写",
  "handler": {
    "executable": "/绝对路径/已安装的宿主入口",
    "args": ["固定参数"],
    "timeoutSeconds": 60,
    "batchLimit": 20,
    "stateFile": "/已有私有目录/watch-state.json"
  }
}
```

executable 是真实程序的绝对路径，不是 shell 命令串；CLI 不拼接 shell。
timeoutSeconds 为 1–300，batchLimit 为 1–20。stateFile 必须是规范绝对路径，
父目录必须已存在且在 Unix 上为 0700；状态和固定 .lock 文件为 0600。
Windows 需将目录放在本用户专属、受系统 ACL 保护的位置；CLI 不配置 Windows ACL。
不把凭证放 args 或状态路径。普通 watch 不运行 handler，需显式启用 --dispatch。

入口 stdin 接收一个 JSON 对象：

```json
{
  "type": "starry_notifications",
  "mode": "notify",
  "task": {
    "id": "task_实际ID",
    "notificationIds": ["9007199254740993"],
    "state": "running",
    "reason": "",
    "updatedAt": "2026-11-03T09:00:00Z"
  },
  "items": [],
  "principal": {"ownerUserId": "主账号", "integrationId": "连接", "credentialId": "凭证标识", "scopes": ["community.read"], "grantVersion": 1},
  "allowedActions": []
}
```

items 是派发前重新校验相同授权、重新读取当前页后仍存在的通知，最多 batchLimit 条。
不可见或已删除目标不能靠历史正文补齐。ID 始终为十进制字符串。
输入不含 API Key、JWT 或配置路径。子进程只继承 PATH/HOME/USERPROFILE/SYSTEMROOT/
TEMP/TMP/TMPDIR/LANG。宿主需要自己的可信连接；默认只提醒，通知不授予发布权限。

入口 stdout 只能输出一个 JSON 对象，最多 64 KiB，退出码为 0；诊断写 stderr：

```json
{"taskId":"task_原ID","state":"completed","reason":"no_action"}
```

state 为 completed/failed/unknown；reason 的闭集见 [命令参考](commands.md)。
缺少上下文可返回 failed/insufficient_context，结果不明返回 unknown/outcome_unknown。
错误 ID、多余字段或 JSON、非零退出、输出超限、超时均保存 unknown 并停止。
CLI 仅输出 task_started/task_completed 元数据，不转发宿主自由文本或正文。
宿主负责自己启动的子进程；CLI 取消直接启动的入口进程。

任务调用前原子写入 running，写盘失败不调用宿主；回执原子写入本机状态。
记录绑定后端、主账号、连接和 executable/args。整个 dispatch watch 持有操作系统独占锁，
保护使用同一文件的本机进程；崩溃自动释放锁，但 running/unknown 不自动重派。
同一更新串行执行最多 5 个任务、扫描最多 100 页。空结果不调用模型。
扫描从首页开始，通知已读状态不作为处理进度；不按最大 ID 忽略迟提交通知。
达到预算时保留记录、退出且不推进 inbox cursor，下一次获准运行从首页继续去重扫描。
通知历史超过扫描预算时需要宿主管理自己的消费范围；CLI 不承诺无限历史补发。

恢复前停止持锁的 watch，用 `watch-state --state-file /私有目录/watch-state.json`
查看 running/unknown。核实宿主实际结果，若有单独获准的发布则查原 operation，
再以 [resolution.json](commands.md) 显式设为 completed 或 failed。
已有 completed/failed 不可覆盖。不要删除状态文件或换文件绕过去重。
记录只含任务 ID、通知 ID、状态、原因和时间，不保存正文或凭证；上限 16 MiB，
损坏或满容量时停止，不自动清空旧 ID。备份和修复须保留去重记录。

不同设备、状态文件或 MCP 宿主独立管理处理进度；不提供服务端任务抢占或同步。
task_completed 只表示本机提醒回执已保存；已授权发布仍由宿主保留原意图，
后端现有 operation、审核和配额继续生效，不代表内容已公开。
