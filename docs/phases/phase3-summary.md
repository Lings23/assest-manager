# 阶段三工程化改造状态

## 当前结论

- 记录日期：2026-10-12
- 状态：阶段三基础功能及 AMQP、独立 publisher/worker、执行租约改造已完成本地验收
- 范围：任务中心、导入导出、SQLite 历史迁移、权限统计读模型

## 交付与证据

2026-08-28 的耗时、数量与验收结论属于原 HTTP Broker 实现，不能作为新版 AMQP 验收结果。新版的 18 项可靠性证据、复现命令和环境说明见 [AMQP 本地验收记录](../architecture/amqp-worker-publisher-acceptance.md)，业务切换和回退步骤见 [AMQP 迁移方案](../architecture/amqp-worker-publisher-plan.md)。本次完成本地验证，未切换业务数据。

| 范畴 | 完成内容 | 主要证据 |
|---|---|---|
| 可靠任务中心 | API、AMQP publisher、worker 分进程；条件更新认领、token/租约、调度代次、独立恢复；Confirm/Return 与提交后 ACK | `platform/internal/taskcenter/broker_amqp.go`、`publisher.go`、`store_execution.go`、`store_outbox.go`、`worker.go` |
| MinIO 异步导入 | CSV 源文件按用户/幂等键/内容摘要写入 MinIO，任务 JSONB 仅存对象引用；Worker 流式读取，预检错误以 JSONL 保存并授权下载 | `platform/internal/taskcenter/service.go`、`worker.go`、`reporting.go`、`docs/architecture/minio-import-export.md` |
| 异步导出 | 权限过滤分页读取、后台生成 CSV、MinIO 持久化和仅任务所有者可下载 | `platform/internal/taskcenter/asset_client.go`、`object_store.go`、`reporting.go` |
| 对象治理 | 导入源、错误报告、导出结果统一配置可调保留期；数据库明确未提交与幂等竞争时清理孤儿对象 | `platform/internal/taskcenter/object_store.go`、`compose.yaml` |
| 历史迁移 | SQLite 只读 CLI、dry-run、断点、字段/布尔/枚举/日期纠偏、软删除保留、错误报告和目标端旧主键幂等 | `tools/sqlite-migrate/`、`platform/internal/asset/legacy.go` |
| 统计读模型 | 按资产类型实时刷新；以请求用户和部门/数据范围/敏感权限指纹隔离快照 | `platform/internal/taskcenter/reporting.go`、`migrations/001_init.sql` |
| 契约与部署 | 导入、导出、下载、任务、报表及迁移 API；Gateway 100 MiB 导入上限；RabbitMQ、MinIO、PostgreSQL Compose | `api/openapi/v1.yaml`、`compose.yaml` |
| 执行观测 | publisher/worker 的 `/metrics` 提供 Outbox、Confirm/Return/NACK、Claim 冲突、租约恢复、执行耗时、未确认消息及死信/隔离指标 | `platform/internal/taskcenter/metrics.go` |

## 2026-10-12 AMQP 本地验收

- 真实隔离 PostgreSQL 上的资产全包测试通过，覆盖 8 路重叠批次重放、legacy 并发重放、数据库失败后占位回滚和安全重试；两处 advisory lock 已删除。
- 认领、续租、任务行、进度、完成、重试和永久失败均校验执行 token 与有效租约；真实 PostgreSQL 回归验证行锁等待后才分配认领租约，过期执行者不能续活或覆盖新执行。
- 真实资产事务提交后关闭 HTTP 响应，重放保持资产/版本/占位各 2 条，结果为 created=0、skipped=2、committed=2；迟到的旧 S3 PUT 不改变新执行的下载内容。
- Publisher 实际子进程在真实 Confirm 后、数据库回写等待行锁时被强杀；11 秒测试认领租约自然到期后，同 event ID 重发两次，仅产生一次业务执行。
- 永久输入错误首次执行即事务死信；基础设施临时故障最多执行 3 次。重试/死信、Inbox 与 Outbox 同事务提交，提交成功后才 ACK。

| 验收项 | 新版实际结果 |
|---|---|
| Go 工具链与全量检查 | 显式使用 Go 1.25.13，根模块与 `platform` 全量 `go test ./... -count=1`、`go vet ./...` 通过；最新任务中心测试耗时 22.080 秒，包含真实 PostgreSQL/RabbitMQ |
| 标准镜像与扩容 | 原始 `platform/Dockerfile` 多阶段构建全部 Go 服务并健康启动；2 个 worker 副本、4 个独立共享队列消费者，manual ACK、prefetch=1 |
| 全链路 Compose | 导入 1、累计提交 1、导出 100,005 行；导出 key 按 token 隔离；Inbox 3、死信 1、权限统计快照 7 |
| 10 万行导入 | 完整链路一次本地测量 19.57 秒；累计提交与导入幂等键均为 100,000，首次尝试成功；此数据不是 p95，也不用于与旧耗时直接比较 |
| 历史迁移回归 | legacy_keys=2、departments=1、corrected_soft_deleted_data=1；删除本地断点后重放 repeat_created=0 |
| RabbitMQ 中断 | API 仍 ready，pending Outbox=1；重启后恢复派发并在 attempt=1 完成 |
| PostgreSQL 中断 | 未持久化消息保留投递能力，重启后隔离记录恰好 1 条 |
| MinIO 临时故障 | 临时错误执行 3 次后进入死信，持久 dead Outbox=1 |
| 强杀 worker | 实际强杀 2 个 worker 后，租约自然过期恢复；attempt=2、dispatch_version=2，导出 200,005 行 |

以上新版结果与原实现历史结果分别保留；完整场景与测试名称见 [18 项验收证据](../architecture/amqp-worker-publisher-acceptance.md#方案第-13-节逐项证据)。

## 历史验收结果（原实现）

| 验收项 | 结果 |
|---|---|
| 10 万行真实导入 | 100,000 行经 HTTP 预检、RabbitMQ worker 和 PostgreSQL 批量提交全部成功；耗时 18.68 秒；幂等键与提交行均为 100,000 |
| 全链路 Compose | 导入 1、导出 100,004 行、失败任务重试 3 次后进入死信；Inbox 9、死信 3、导入幂等键 100,004、权限统计快照 7 |
| MinIO 补强 Compose 回归 | 2026-08-28 复验通过：导入源对象引用不含原始 CSV、Worker 成功提交；无效导入生成并下载 JSONL；导出 100,007 行；任务服务带生命周期配置健康启动 |
| 迁移样本 | 2 个旧主键只生成 2 个目标映射；责任部门关键字段一致；数据资产枚举“二级”修正为 1、布尔“是”修正为 true、软删除保留；删除本地 checkpoint 后重复执行创建 0 条 |
| Go | 根模块与 `platform` 的 `go test ./...`、`go vet ./...` 通过 |
| Vue | `npm run typecheck`、Vitest 3 项和生产构建通过 |
| 生成一致性 | Contract 与 Schema 生成器 `-check` 通过 |

## 2026-08-28 MinIO 补强

- 导入原文已从 PostgreSQL `task.input.csv` 迁出，避免大字段放大 WAL、备份和任务查询成本。
- 新增 `GET /api/v1/import-jobs/{id}/errors`，错误报告以 NDJSON 保存在 MinIO，下载同时校验 Token 与任务所有者。
- 新增 `S3_RETENTION_DAYS`（默认 7 天）和 `imports/`、`import-errors/`、`exports/` 三条生命周期规则。
- 补齐创建任务时的部门数据范围校验，并阻止带预检错误的任务确认。
- Worker 兼容读取改造前存量任务中的 `input.csv`，新任务只读取 `source_object_key`。
- 平台 `go test -count=1 ./...`、`go vet ./...`、Contract `-check` 与真实 Compose 冒烟均通过。

可重复验收脚本：

- `scripts/phase3-compose-smoke.ps1`
- `scripts/phase3-capacity.ps1`
- `scripts/phase3-migration-smoke.ps1`
- `scripts/amqp-compose-fault-smoke.ps1`（仅用于隔离的 `asset-amqp-*` 项目）

## 阶段退出判定

1. 10 万行导入容量测试通过，预检采用流式读取，确认后分批提交。
2. 迁移样本数量、关键字段、枚举/布尔纠偏和软删除状态对账一致。
3. 任务级、消息级、导入行级和旧主键级幂等均有验证，重试不产生重复数据。

基础阶段三及新版 AMQP 改造的本地退出条件均已满足。部署到已有 HTTP consumer 环境时，仍须执行方案中的受控停机、迁移及切换步骤；不能将新旧消费者混跑，或直接回退到不校验 token 的旧 worker。
