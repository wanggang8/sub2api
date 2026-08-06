# Observer 数据批量导出设计

## 状态

已确认，待实施。

## 目标

在 sub2api 内嵌的 observer 控制服务中增加受独立管理 token 保护的数据批量导出能力。一次导出包含导出开始前已经落盘的 observation 上传包和 agent 心跳记录；导出成功后保留合并压缩包并清理本批次原文件，同时不阻塞后续心跳和采集上传。

## 非目标

- 不把导出权限加入 observer agent token。
- 不把原始导出 token 编译进 observer 或 sub2api 二进制。
- 不增加管理页面、定时导出、对象存储或自动过期策略。
- 不改变现有心跳、上传、版本查询和发布物下载协议。

## 认证与密钥

导出接口使用独立 Bearer token。sub2api 只读取部署环境变量 `OBSERVER_EXPORT_TOKEN_SHA256`，值为 `sha256:<64 位十六进制>`；运行时对请求 token 计算 SHA-256 并进行常量时间比较。

原始 token 由维护方生成，可保存在 observer 项目 Git 已忽略且权限为 `0600` 的 `local-evidence/embedded-release-secrets/observer-export.token`。其 SHA-256 可保存在同目录的 `observer-export-token.sha256`，并作为 sub2api 服务器环境变量配置。原始 token、真实数据和摘要均不提交 Git，也不进入发布物。

未配置或配置非法时，sub2api 仍可启动，现有 observer 接口不受影响；导出接口统一返回 `503 export_not_configured`。agent token 调用导出接口返回 `401 unauthorized`。

## HTTP 接口

### 创建并下载批次

`POST /api/v1/observer/exports`

请求必须携带 `Authorization: Bearer <export-token>`，无请求体。成功返回 `201 Created` 和 `application/gzip` 响应体，并设置：

- `Content-Disposition: attachment; filename="observer-export-<export_id>.tar.gz"`
- `X-Observer-Export-ID: <export_id>`
- `X-Checksum-SHA256: sha256:<hex>`

没有可导出文件时返回 `409 no_exportable_data`，不生成空包。服务端同一时间只创建一个批次；并发创建返回 `409 export_in_progress`。

### 重复下载

`GET /api/v1/observer/exports/{export_id}`

使用相同的独立导出 token，返回已经成功落盘的不可变压缩包。不存在或 ID 非法时返回 `404 export_not_found`。该接口无清理副作用。

## 压缩包格式

服务端生成 `tar.gz`，成员名固定且禁止绝对路径、路径穿越、链接和设备文件：

```text
manifest.json
observations/<upload_id>.tar.gz
agents/<installation_id>.json
```

`manifest.json` 包含 schema 版本、export ID、创建时间、每个成员的名称、大小和 SHA-256。成员按名称排序，文件权限固定为 `0600`，时间统一使用 UTC。

## 存储布局

```text
observer-control/
  observations/       # 当前尚未导出的采集包
  agents/              # 当前最新心跳
  exports/             # 已完成、可重复下载的批次
  export-receipts/     # 已导出 upload_id 的小型凭据
```

导出包使用临时文件写入、`fsync`、校验后原子重命名为 `exports/<export_id>.tar.gz`，权限固定为 `0600`。压缩包当前永久保留，不自动过期。

## 并发、清理与幂等

1. 在短临界区内快照当前 `observations` 文件列表和 `agents` 文件内容，然后释放锁；新上传和新心跳继续正常落盘。
2. 只把快照成员写入本次压缩包。导出期间新出现的文件留到下一批。
3. 压缩包完整写入、关闭、重新计算 SHA-256 并原子落盘后，才进入清理阶段。
4. observation 文件不可变，只删除快照中且内容仍与快照一致的文件。每个已导出 upload ID 写入小型 receipt；后续相同上传仍返回原 upload ID，不重新保存。
5. agent 心跳文件可能被并发更新；仅当当前文件内容仍与快照一致时删除。已更新的心跳保留到下一批。
6. 清理失败不影响压缩包下载；记录错误并在下一次创建导出前重试清理。不得因清理失败覆盖或删除已完成压缩包。

服务端完成响应写入只能证明传输过程未报告错误，不能证明客户端已持久化文件，因此原文件清理以“服务端压缩包已验证并持久化”为边界，而不是以客户端连接状态为边界。已经保留的压缩包可通过 GET 接口重新下载。

## 错误与恢复

- 快照或打包失败：不清理源文件，不暴露临时文件。
- 临时包校验失败：删除临时包，保留源文件。
- 客户端中断：已完成压缩包继续保留，可按 export ID 重试下载。
- 进程在原子重命名前退出：启动或下次导出时清理过期临时文件，源文件仍在。
- 进程在重命名后、清理前退出：已完成压缩包可下载，下次导出先按 manifest 幂等补做清理。
- 非普通文件、symlink、超出数据目录的路径或校验变化：拒绝纳入或删除，并记录错误。

## 实现边界

变更集中在 `backend/internal/observercontrol`，仅在现有路由注册处接入两个接口和配置读取。observer 客户端无需修改或重新嵌入导出 token；升级发布只需要包含更新后的 sub2api 二进制。

## 验证

- 独立 token 成功、缺失、错误、agent token 越权测试。
- observations 与 agents 同时导出，manifest、成员、大小和 SHA-256 校验测试。
- 空批次、非法 export ID、重复下载和并发创建测试。
- 导出期间上传 observation、更新 heartbeat，验证新数据不被清理且下一批可导出。
- 打包失败、原子落盘失败、响应中断和清理失败注入测试。
- 清理后重复上传同一 observation，验证 receipt 保持幂等。
- symlink、非普通文件、路径穿越和文件替换竞态测试。
- `go test ./backend/internal/observercontrol ./backend/internal/server/routes`、全量 `go test ./...`、`go vet ./...` 和 Linux amd64 发布构建。
