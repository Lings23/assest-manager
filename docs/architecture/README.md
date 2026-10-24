# 新平台 Monorepo 工程基线

## 目录边界

```text
apps/web/                    Vue 3 + TypeScript 前端
api/openapi/                 公共 REST 契约单一入口
api/generated/               OpenAPI 资产 Schema 生成产物，禁止手工修改
schemas/assets/              七类资产 YAML 定义单一来源
platform/cmd/gateway/        API Gateway 进程
platform/cmd/iam-service/    身份与权限领域进程
platform/cmd/asset-service/  资产与 Schema 领域进程
platform/cmd/governance-service/ 治理领域进程
platform/cmd/task-report-service/ 任务与报表领域进程
platform/cmd/task-publisher/ 独立 AMQP Outbox 发布进程
platform/cmd/task-worker/    独立 AMQP 消费、执行与租约恢复进程
platform/internal/iam/       IAM 领域实现与数据库迁移
platform/internal/asset/     Schema 驱动资产引擎与数据库迁移
platform/internal/taskcenter/ 任务、Outbox/Inbox、执行租约与 AMQP 适配
platform/internal/servicekit/ 服务公共工程基线
deploy/                      Compose 初始化和后续部署资产
internal/、web/、main.go      已冻结的旧系统
```

每个服务只允许访问自己拥有的 PostgreSQL Schema。跨服务读取必须通过 `/api/v1` REST API，状态传播使用 RabbitMQ 领域事件；禁止跨 Schema 直接查询。资产服务通过 IAM 授权接口获取实时用户状态和数据范围，不直接读取 `iam` Schema。

异步文件链路详见 [MinIO 异步导入导出设计](minio-import-export.md)。
任务运行时与切换流程详见 [AMQP、独立 Publisher 与 Worker 原子认领方案](amqp-worker-publisher-plan.md)。
实现、逐项故障证据及容量实测见 [AMQP 改造验收记录](amqp-worker-publisher-acceptance.md)。publisher/worker 的内部 `/metrics` 提供发布确认、Outbox、认领冲突、租约恢复、执行耗时和未确认消息指标。

本机性能基线的场景、数据集、采集口径和实施顺序见 [Docker Compose 压力测试方案](../testing/local-compose-performance-plan.md)。

负载均衡选型、HTTP 多副本前置改造、任务预检恢复、连接预算及分阶段验收见 [负载均衡与多实例改造调研](load-balancing-multi-instance-research.md)。

HAProxy Community 与 Nginx OSS 的能力比较、选型理由及 Go Gateway 迁移边界见 [HAProxy 与 Nginx 选型](haproxy-vs-nginx-selection.md)。

## 本地启动

1. 复制 `.env.example` 为 `.env` 并替换所有开发密码。
2. 执行 `docker compose up --build -d`。`task-migrate` 先以 `--migrate-only` 执行任务库增量迁移，退出成功后三个任务进程才启动；任务运行时只连接数据库并检查迁移版本。
3. 访问前端 `http://localhost:8088`，网关健康检查为 `http://localhost:8080/health/ready`。
4. 执行 `bash scripts/compose-smoke.sh` 验证所有容器与网关路由。
5. 停止环境使用 `docker compose down`；除非明确要清空开发数据，不要附加 `-v`。

已有 HTTP consumer 部署应先按迁移方案停旧执行者并处理存量任务，再启动新 publisher/worker，避免旧新版本混跑。`task-report-service` 仅处理 API；`task-publisher` 只连接 PostgreSQL 与 AMQP；`task-worker` 默认 2 个 slot，每个 slot 使用共享队列订阅、手动 ACK 和 `prefetch=1`。可用 `docker compose up -d --scale task-worker=2` 扩容，worker 不绑定固定宿主端口。

worker 默认租约 90 秒、每 20 秒续租、任务上限 20 分钟、每 10 秒恢复过期执行、退出等待 60 秒；Compose 停机宽限 75 秒。RabbitMQ 配置显式设置确认超时 30 分钟，调整任务上限时应同步检查该预算。Management API 只用于管理界面和冒烟只读检查，业务运行使用 `RABBITMQ_URL`。旧格式消息兼容开关默认关闭。

## 工程约定

- 所有服务启动前校验配置，统一暴露 `/health/live` 和 `/health/ready`。
- 公共错误使用 `error.code/message/request_id`，响应和日志都携带 `X-Request-ID`。
- HTTP 服务设置读取、写入、空闲和优雅停机超时。
- Access Token 仅保存在 Vue/Pinia 内存中，Refresh Token 使用 HttpOnly Cookie 并在服务端轮换。
- API 或资产 Schema 修改后运行 `go generate .`，CI 同时检查 Contract 和 Schema 生成漂移。
