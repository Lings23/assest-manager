# 资产治理平台 / 旧资产信息管理系统

本仓库同时包含迁移期旧系统和新资产治理平台：

- 根目录 `main.go`、`internal/`、`web/`：Go、SQLite 和嵌入式前端组成的旧系统，已进入功能冻结。
- `platform/`、`apps/web/`、`api/`、`deploy/`：新平台 Monorepo 工程基线，新业务统一在此建设。

阶段一完成了旧系统安全止血以及新平台工程骨架。当前阶段和验收证据见[改造状态](改造状态.md)，总体路线见[工程化改造计划](工程化改造计划.md)。

## 功能范围

旧系统当前承担实际业务，管理以下七类资产：

1. 信息系统清单
2. 信息化软硬件清单
3. 数据资产清单
4. 供应链清单
5. 风险漏洞清单
6. 软件信息统计
7. 责任部门

已实现能力包括：

- 管理员和填报员登录，密码使用 bcrypt 保存。
- 七类资产的新增、查询、更新、分页、搜索和软删除。
- 填报员仅访问本人创建的数据，删除仅允许管理员执行。
- CSV 模板、导入和导出，支持中文表头、枚举及布尔转换。
- 概览、系统、软硬件和漏洞统计，以及软件采购累计计算。

旧系统没有完整的用户管理、审计写入、自动备份恢复和 Excel 导出；这些能力不能仅根据历史文档或空目录视为已经实现。

新平台阶段一只提供可运行的工程骨架，IAM、资产 Schema、审批、任务和报表业务将在后续阶段实现。

## 环境要求

- Go 1.25.12 或更新的受支持版本
- Node.js 22
- Docker Engine 和 Docker Compose v2

## 新平台本地启动

复制示例配置并替换全部占位密码：

```powershell
Copy-Item .env.example .env
docker compose up --build --detach --wait
```

访问地址：

- 新前端：`http://localhost:8088`
- API Gateway：`http://localhost:8080`
- 网关就绪检查：`http://localhost:8080/health/ready`

完整冒烟：

```powershell
.\scripts\compose-smoke.ps1
```

脚本会生成临时测试密码、构建并启动完整环境、执行 HTTP 和容器健康检查，然后停止容器。

## 旧系统本地启动

正式环境必须显式配置安全的 `JWT_SECRET` 和 `ADMIN_PASSWORD`。本地开发未配置时会生成进程级临时值，重启后 Token 会失效；首次启动生成的管理员临时密码只输出到启动日志。

```powershell
$env:JWT_SECRET = 'replace-with-at-least-32-random-characters'
$env:ADMIN_PASSWORD = 'replace-with-at-least-12-random-characters'
go run .
```

旧系统当前默认监听 `http://localhost:8082`，可通过 `PORT` 修改。不存在固定的 `admin123` 默认密码。

## 常用命令

```powershell
# OpenAPI 生成一致性
go run ./tools/contractgen -check

# 旧系统测试
go test -count=1 ./...

# 新平台测试
Push-Location platform
go test -count=1 ./...
Pop-Location

# Vue 类型检查和生产构建
Push-Location apps/web
npm ci
npm run typecheck
npm run build
Pop-Location
```

也可以使用 Makefile：

```bash
make check
make test
make build
make compose-smoke
```

## 目录

```text
api/openapi/                 公共 REST 契约
apps/web/                    Vue 3 + TypeScript 前端
platform/cmd/gateway/        API Gateway
platform/cmd/iam-service/    IAM 服务骨架
platform/cmd/asset-service/  资产服务骨架
platform/cmd/governance-service/ 治理服务骨架
platform/cmd/task-report-service/ 任务与报表服务骨架
platform/internal/servicekit/ 公共工程能力
deploy/                      Compose 初始化和部署资产
docs/                        架构、冻结、分支及安全规范
internal/、web/、main.go     已冻结的旧系统
```

## 开发规则

- 新业务不得继续加入旧系统目录，冻结规则见[旧系统功能冻结](docs/legacy-freeze.md)。
- 分支和发布流程见[分支与发布策略](docs/development/branching-strategy.md)。
- 密钥处理规则见[密钥与敏感配置管理](docs/security/secrets.md)。
- OpenAPI 修改后运行 `go generate .`，CI 会检查生成产物是否漂移。
- 前端和后端不得提交 `.env`、数据库、私钥、Token、运行日志或构建产物。
