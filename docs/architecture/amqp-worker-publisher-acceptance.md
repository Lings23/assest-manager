# AMQP、独立 Publisher 与 Worker 执行权验收

日期：2026-10-12。范围与切换约束见 [迁移方案](amqp-worker-publisher-plan.md)。本文记录新版直接证据，旧 HTTP Broker 的数量和耗时另保留为历史记录。

## 实现范围

API、publisher、worker 已分为三个进程。API 不初始化 Broker；publisher 独立认领 Outbox 并等待 AMQP Confirm/Return；worker 以共享队列、手动 ACK、prefetch=1 和有限 slot 执行。独立恢复器不受执行循环阻塞。

任务认领是单条 SQL：materialized CTE 先锁主键行，再条件 UPDATE 并计算租约，避免等待未变化行锁时提前消耗租约。成功 Claim 后提交事务，网络副作用不持有数据库事务。续租、进度、任务行、完成、重试、永久失败及 Outbox 回写在锁等待后用新数据库时间校验 token、worker 和租约。只有 Claim 增加 attempt；重试与恢复递增调度代次，重试/死信/Inbox 同事务。

预检及预检失败保持代次 0；导入 staging 合并保留 committed_at。资产与 legacy 的唯一键占位、资产、版本同事务，两处 advisory lock 已删除。导出使用 `exports/{task_id}/{execution_token}.csv`，完成事务决定下载引用。非法二进制/NUL/NaN 消息也能持久隔离，并忽略重投标志变化去重。

DDL 只由 task-report-service 的 `--migrate-only` 执行；运行时只连接并检查版本。迁移校验和统一 LF，并兼容历史 LF/CRLF 登记；仅显式迁移规范化登记，实际 SQL 内容变化仍拒绝。HTTP 任务响应使用独立 `TaskDTO`，不包含内部代次、token、worker 或租约字段。旧数据切换使用 `--migrate-only --cutover-old-tasks`，必须先停止全部旧执行者。本次验收使用隔离测试数据。

## 方案第 13 节逐项证据

任务测试源位于 `platform/internal/taskcenter/`，资产测试源位于 `platform/internal/asset/`。

| 场景 | 直接证据 |
| --- | --- |
| 同任务同代次竞争 | `TestPostgresAtomicClaimAndOldExecutionFencing`：16 个竞争者仅一次 Claim、attempt=1；真实 Worker/PG/AMQP 重复通知测试 |
| 不同任务并行 | `TestWorkerPostgresAMQPConcurrentDuplicateRecoveryAndDrain`：两个 slot 同时执行，执行繁忙时恢复器仍推进 |
| Claim 后杀 worker | `amqp-compose-fault-smoke.ps1`：实际 SIGKILL 两个实例，租约自然到期后 attempt=2、dispatch_version=2 完成；并发恢复次数耗尽测试 |
| 提交成功、ACK 前断线 | `TestWorkerPostgresAMQPDisconnectAfterCommitDeduplicatesRedelivery`：真实数据库完成提交后关闭原 AMQP socket，重投仅一次执行 |
| 资产提交后响应丢失 | `TestPostgresAssetCommitWithLostHTTPResponseReplaysCumulativeProgress`：真实资产事务提交后关闭 HTTP 连接，第二代 created=0/skipped=2/committed=2，资产/版本/占位各 2 条 |
| 旧执行者恢复写入 | 旧 token 的续租、任务行、进度、Complete、Retry 和永久失败全拒绝；另验证等待未变化行锁超过租约后的拒绝 |
| 旧导出迟到 | `TestPostgresLateS3UploadCannotReplaceCurrentDownload`：签名 S3 HTTP 的旧 PUT 阻塞到新执行完成后才落盘，旧 Complete 拒绝，Service.Download 实际返回新内容 |
| Confirm 后杀 publisher | `TestPublisherProcessKillAfterConfirmBeforeDatabaseMark`：真实子进程 Confirm 后，pg_stat_activity/pg_blocking_pids 证明回写等待父进程行锁；OS 强杀、11s 测试租约自然到期，同 event ID 两次真实投递仅一次业务执行 |
| publisher 并发/迟到回写 | 32 个 publisher 互斥认领；旧 claim 与锁等待到期的成功/失败回写均被拒绝 |
| Return/NACK/紧邻通知 | 真实 mandatory Return、reject-publish 队列真实 NACK、1000 次同时就绪通知选择；Outbox 保留并重连重发 |
| 退避与旧代次 | next_run_at 数据库条件、旧代次拒绝；worker 用数据库观测时间退避并重建 slot，避免即时重投循环 |
| PostgreSQL/RabbitMQ 中断 | Compose 实际停 PostgreSQL 时消息保留，恢复后隔离一次；停 RabbitMQ 时 API 仍就绪、Outbox 保留、重启后自动派发 |
| 失败/死信事务后退出 | failed/dead、Inbox、Outbox 一起提交；最后一步非法 Inbox UUID 触发所有状态回滚；永久失败第 1 次即死信，暂时故障最多 3 次 |
| 非法消息/不存在任务 | quarantine/dead Outbox 持久化，不依赖 dead_letters 外键；原指纹去重，二进制/NUL/NaN header 回归 |
| 同步预检未确认 | 不被异步认领/恢复；可重入 cutover 将中断预检标 failed、代次 0，保留历史 attempt |
| 重叠批次/legacy 并发 | 8 路真实 PG 重放，失败事务不残留占位；保留软删除映射，无重复资产/版本 |
| SIGTERM/强制超时 | 真实 Worker/PG/AMQP drain 保持执行 context 与续租；超时保留可恢复执行及未确认消息 |
| 慢任务期间新任务 | 三进程独立生命周期、繁忙 slot 的独立恢复测试；Compose 容量任务期间 API 和历史迁移仍成功 |

迟到 S3 测试使用真实签名客户端与可控 HTTP 对象服务注入延迟；完整文件持久化、下载和生命周期另在真实 MinIO Compose 验证。Publisher 强杀是实际子进程终止，未增加生产调试钩子。

## 环境、版本与复现

AMQP 客户端固定 amqp091-go v1.11.0，依赖该版本同步 Return 分发后再处理 Confirm 的顺序，单 channel 一条在途。升级前需重新验证通知屏障和恢复责任。生产配置：publisher lease=30s/confirm=10s；worker 2 slots、lease=90s、renew=20s、recovery=10s、task=20m、drain=60s；consumer timeout=30m，启动时检查 task timeout+1m；Compose grace=75s。

组件环境为 PostgreSQL16/RabbitMQ3.13，端口 55432/55672。完整环境为独立项目 `asset-amqp-acceptance`，`deploy/test/amqp.env`，网关58080/Web58088，两个 worker 实例共4slots。

初次拉取 Go builder 遇到 Docker Hub IPv6 鉴权超时，中途使用本地静态二进制继续组件验收。随后固定 builder digest 拉取成功，**最终已用原始 `platform/Dockerfile` 的 Go1.25.13 多阶段构建全部服务，并以标准 Compose 启动**；最终容器结果来自此镜像。主机完整测试也显式使用 Go1.25.13，二进制元数据已核实；最初默认 Go1.26.5 的结果不作为最终工具链证据。临时运行时替代文件和二进制已清理。

组件测试：

```powershell
docker compose -f deploy/test/compose.amqp.yaml up -d --wait
$env:GOTOOLCHAIN='go1.25.13'
$env:TASKCENTER_TEST_DATABASE_URL='postgres://amqp_test:amqp_test_local@127.0.0.1:55432/amqp_test?sslmode=disable'
$env:TASK_TEST_DATABASE_URL=$env:TASKCENTER_TEST_DATABASE_URL
$env:ASSET_TEST_DATABASE_URL=$env:TASKCENTER_TEST_DATABASE_URL
$env:TASKCENTER_AMQP_TEST_URL='amqp://amqp_test:amqp_test_local@127.0.0.1:55672/'
Push-Location platform
go test ./... -count=1
go vet ./...
Pop-Location
```

标准启动为 `docker compose up --build -d`。本次隔离项目的完整复现：

```powershell
$env:GOTOOLCHAIN='go1.25.13'
$env:GODEBUG='netdns=go'
docker compose --env-file deploy/test/amqp.env -p asset-amqp-acceptance up -d --build --wait --scale task-worker=2
pwsh -File scripts/phase3-compose-smoke.ps1 -EnvFile deploy/test/amqp.env -ComposeProject asset-amqp-acceptance -BaseUrl http://127.0.0.1:58080/api/v1
pwsh -File scripts/phase3-capacity.ps1 -Rows 100000 -EnvFile deploy/test/amqp.env -ComposeProject asset-amqp-acceptance -BaseUrl http://127.0.0.1:58080/api/v1
pwsh -File scripts/phase3-migration-smoke.ps1 -EnvFile deploy/test/amqp.env -ComposeProject asset-amqp-acceptance -BaseUrl http://127.0.0.1:58080/api/v1
pwsh -File scripts/amqp-compose-fault-smoke.ps1 -EnvFile deploy/test/amqp.env -ComposeProject asset-amqp-acceptance -BaseUrl http://127.0.0.1:58080/api/v1
```

故障脚本只接受 `asset-amqp-*` 项目，停止基础设施并强杀 worker，finally 恢复基础设施。业务切换仍须按方案停旧进程、迁移数据，不能直接混跑或回退到无 token 的旧消费者。

## 监控

publisher内部8085和worker内部8086提供 `/metrics`，无需 Management API：Outbox待发送量/最老年龄、Confirm/Return/NACK/连接失败、Claim冲突、租约恢复次数、执行直方图、未确认消息、死信/隔离数量。

数据库 gauge 是跨副本共享事实，应 max/去重；进程 counter、直方图和本进程未确认消息按实例抓取后聚合。断线已交回 Broker 的消息不计入本进程 gauge。数据库不可用时按请求期限或3s返回503，避免零值伪装健康。

## 最终复验输出

Go1.25.13 下根模块和平台的 `go test ./... -count=1`、`go vet ./...` 全部通过；最终任务中心测试包含真实依赖、实际 publisher 进程强杀、公共 DTO 和迁移换行兼容，耗时22.080s。Contract/Schema生成器 `-check` 通过；标准 Docker多阶段构建及全部服务就绪通过；publisher和第二worker副本的 `/metrics` 实际抓取通过。

最终镜像的实际任务详情、导入列表（10条）和导出列表（13条）均未暴露内部执行字段；容量任务仍返回 succeeded、committed=100000。一次性迁移正常退出，001/002 校验和均已登记。

标准 Compose 的最终业务与故障输出：

```text
Phase 3 Compose smoke passed: import=1 committed=1 export_rows=100005 token_key=exports/2e9b23cc-2a3d-46ef-8bb0-591f07444aa3/c0fa325c-7bfb-46cd-96e9-469c1f97f4fa.csv consumers=4 manual_ack=true prefetch=1 attempts=1 inbox=3 dead=1 import_keys=1 report_rows=7
Phase 3 capacity passed: rows=100000 seconds=19.57 task=500a10a0-88e2-4c31-9126-6d9585c3f631 import_keys=100000 committed_rows=100000
legacy_keys=2 departments=1 corrected_soft_deleted_data=1 repeat_created=0
RabbitMQ outage passed: api_ready=true pending_outbox=1 resumed_attempt=1
PostgreSQL outage passed: delivery_retained=true quarantine_after_restart=1
MinIO outage passed: transient_attempts=3 dead_outbox=1
SIGKILL recovery passed: task=85eeab02-731a-46ca-8967-5e269b45e8ff attempts=2 dispatch_version=2 rows=200005
```

容量耗时是本机单次责任部门两字段 CSV 的完整提交链路，包含上传、预检、确认、排队、执行和完成轮询；不含生成 CSV、登录、后续重放及数据库对账。该结果不代表 p95，也不用于与旧实现比较。故障后的迁移、API、publisher 和两个 worker 均恢复就绪。方案第13节18项场景已取得上述本地验收证据；业务环境切换仍按停旧实例、显式迁移和启用新实例的步骤执行。

结束时数据库 active_tasks=0、pending_outbox=0。本次创建的 `asset-amqp-test` 与 `asset-amqp-acceptance` 容器和网络已通过 Compose down 清理，未使用 `-v`，完整验收项目的命名数据卷保留；可按上述命令重新启动。
