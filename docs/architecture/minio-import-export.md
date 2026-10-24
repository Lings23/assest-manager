# MinIO 异步导入导出设计

## 目标与边界

任务中心使用 MinIO 保存批量任务的文件负载，PostgreSQL 只保存任务状态、授权快照和对象引用。当前外部契约继续接受 UTF-8 CSV 字符串，以兼容现有 Vue 与脚本；服务收到请求后立即把源文件写入 MinIO，不再把 CSV 原文写入任务 JSONB。

## 对象模型

| 前缀 | 内容 | Key 规则 | Content-Type |
|---|---|---|---|
| `imports/` | 导入 CSV 源文件 | `imports/{userId}/{idempotencyKeyHash16}/{contentSHA256}.csv` | `text/csv; charset=utf-8` |
| `import-errors/` | 预检逐行错误 | `import-errors/{taskId}.jsonl` | `application/x-ndjson; charset=utf-8` |
| `exports/` | Worker 生成的导出文件 | `exports/{taskId}/{executionToken}.csv` | `text/csv; charset=utf-8` |

源对象 Key 同时包含用户、幂等键摘要和内容摘要。同一请求重试得到同一 Key；并发使用同一幂等键但不同内容时，数据库唯一约束决定唯一任务，未被任务引用的竞争对象会立即删除。对象 Key 不包含用户提交的原始文件名，避免路径穿越与敏感信息泄露。

## 导入时序

1. 任务服务完成 `asset:create` 授权，并校验目标部门属于 IAM 数据范围。
2. 按用户、任务类型和幂等键查询已有任务；已存在则直接返回，不重复上传。
3. CSV 写入 `imports/`，任务 `input` 只记录 `source_object_key`、大小和 Content-Type。
4. 服务从 MinIO 流式读取源对象进行预检，任务进入 `awaiting_confirmation`。
5. 错误明细编码为 JSONL 写入 `import-errors/`；任务结果只保留错误数量、对象 Key 和受保护下载地址，避免 JSONB 膨胀。有错误的任务不能确认。
6. 用户确认时，任务调度代次和 Outbox 在同一 PostgreSQL 事务提交。独立 publisher 通过 AMQP Confirm + Return 判定可靠发布；worker 手动确认消息，先用条件更新获得带 token 的执行租约，再从 MinIO 读取源文件并按 500 行提交。

预检阶段的调度代次为 0，恢复器不会将尚未确认的导入交给 worker。任务行 COPY 到 staging 后按唯一键合并，保留已提交的 `committed_at`；写任务行、更新进度与结束任务均在短事务内校验有效租约和执行 token。资产服务以 `(task_id,row_key)` 唯一键原子占位，资产、占位和版本一起提交，不持有 task advisory lock。任务结果的 `created/skipped` 表示本次执行，`committed` 表示任务累计完成行数。

Worker 对改造前已存在的任务保留兼容读取：没有 `source_object_key` 时才读取旧 `input.csv`。新任务不会再写入该字段。

## 导出与下载

导出创建时固化请求人的数据范围。Worker 读取资产数据、生成带 UTF-8 BOM 的 CSV 并写入 `exports/`，任务完成结果保存行数、对象 Key 和下载地址。导出文件与导入错误报告都必须经过任务服务鉴权，且任务查询强制带 `requested_by`，不暴露 MinIO 地址或凭据。

每次执行使用不同 token 对应的导出 Key。只有持有有效租约的完成事务能把该 Key 写入任务结果，下载始终读取结果里的 Key。过期执行者晚完成的上传不会覆盖新文件，未引用对象由 `exports/` 生命周期清理。

## 生命周期与失败处理

任务服务启动时确保 bucket 存在，并为三个前缀写入 S3 Lifecycle 规则。`S3_RETENTION_DAYS` 默认 7，可配置 1–3650 天。该策略用于最终兜底清理源文件、错误报告与导出结果。

- MinIO 上传失败：不创建任务。
- PostgreSQL 明确确认任务未创建：删除本次上传对象；提交结果不确定且无法回读时保留对象，由生命周期兜底，避免误删已提交任务引用的文件。
- 并发幂等竞争失败：仅当竞争对象不是胜出任务引用的对象时删除。
- 预检读取或错误报告写入失败：任务进入 `failed`，调度代次保持 0，错误写入 `last_error`；源对象由生命周期清理，恢复器不会绕过用户确认执行导入。
- Worker 暂时读取失败：校验执行权后，在同一事务安排下一代任务和延迟 Outbox；死信也经事务 Outbox 发布。事务提交后才 ACK 原消息。
- Worker 宕机或失去执行权：取消新增副作用，由独立恢复循环将过期租约安排重试或死信；旧 token 不能续租或修改新执行的结果。

## 当前限制

- 外部 API 仍用 JSON 承载 CSV，Gateway 与任务服务请求体上限为 100 MiB；后续可改为 multipart 或客户端直传。
- `Put` 仍接收完整 `[]byte`，导出生成也在内存聚合；大文件场景应增加流式/分片上传。
- 当前使用 MinIO Root 凭据；生产环境应换成仅允许目标 bucket/prefix 操作的服务账号。
- 下载经任务服务代理，尚未提供短期预签名 URL、对象访问审计和配额告警。
