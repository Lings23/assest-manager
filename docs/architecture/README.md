# 新平台 Monorepo 工程基线

## 目录边界

```text
apps/web/                    Vue 3 + TypeScript 前端
api/openapi/                 公共 REST 契约单一入口
api/generated/               契约生成产物，禁止手工修改
platform/cmd/gateway/        API Gateway 进程
platform/cmd/iam-service/    身份与权限领域进程
platform/cmd/asset-service/  资产与 Schema 领域进程
platform/cmd/governance-service/ 治理领域进程
platform/cmd/task-report-service/ 任务与报表领域进程
platform/internal/servicekit/ 服务公共工程基线
deploy/                      Compose 初始化和后续部署资产
internal/、web/、main.go      已冻结的旧系统
```

每个服务只允许访问自己拥有的 PostgreSQL Schema。跨服务读取必须通过 `/api/v1` REST API，状态传播使用 RabbitMQ 领域事件；禁止跨 Schema 直接查询。阶段一只建立可运行进程和边界，不提前实现阶段二业务。

## 本地启动

1. 复制 `.env.example` 为 `.env` 并替换所有开发密码。
2. 执行 `docker compose up --build -d`。
3. 访问前端 `http://localhost:8088`，网关健康检查为 `http://localhost:8080/health/ready`。
4. 执行 `bash scripts/compose-smoke.sh` 验证所有容器与网关路由。
5. 停止环境使用 `docker compose down`；除非明确要清空开发数据，不要附加 `-v`。

## 工程约定

- 所有服务启动前校验配置，统一暴露 `/health/live` 和 `/health/ready`。
- 公共错误使用 `error.code/message/request_id`，响应和日志都携带 `X-Request-ID`。
- HTTP 服务设置读取、写入、空闲和优雅停机超时。
- Access Token 仅保存在 Vue/Pinia 内存中，后续 Refresh Token 使用 HttpOnly Cookie。
- API 契约修改后运行 `go generate .`，CI 使用 `go run ./tools/contractgen -check` 阻止生成漂移。
