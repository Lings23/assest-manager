# 阶段二工程化改造总结

## 归档信息

- 归档日期：2026-07-23
- 阶段：阶段二（IAM 与元数据资产引擎）
- 状态：实现与本地全栈验收完成，合入 `develop` 后进入阶段三
- 开发基线：`phase1-baseline`
- 实施分支：`codex/phase2-iam-metadata`
- 总体路线：[工程化改造计划](../../工程化改造计划.md)
- 当前状态：[改造状态](../../改造状态.md)
- 权限基线：[新平台权限矩阵](../security/phase2-permissions.md)

## 阶段目标与结果

阶段二把阶段一的空服务骨架变成可实际使用的新平台核心：

1. IAM 使用 PostgreSQL 保存用户、部门和会话，提供 RS256 短期 Access Token、Refresh Token 轮换、复用检测、注销、禁用、权限变更和首次改密。
2. 七类资产由 YAML Schema 统一定义；生成 Go 元数据、OpenAPI Schema、TypeScript 类型、Vue 表单元数据和强类型 PostgreSQL 迁移。
3. 资产服务根据同一 Schema 执行字段白名单、类型/条件校验、查询、数据范围、敏感字段遮罩、软删除、恢复、乐观锁和基础版本记录。
4. Vue 前端使用生成元数据提供七类资产的动态列表与表单，并接入内存 Access Token 和安全 Refresh Cookie 会话。

阶段二退出条件已满足：七类资产都由 Schema 驱动，未知字段不能到达 SQL，Go/OpenAPI/TypeScript/Vue/SQL 生成结果由 CI 检查一致；IAM 与资产核心路径已通过真实 PostgreSQL 和 Compose 端到端测试。

## 方案与选择理由

| 方案 | 选择理由 |
|---|---|
| IAM 独立服务 + PostgreSQL `iam` Schema | 用户状态、权限版本和会话需要跨实例持久化；保持阶段一已确定的领域数据边界。 |
| RS256 15 分钟 Access Token + 不透明 Refresh Token | 私钥只留在 IAM；短期访问令牌降低泄露窗口，不透明令牌便于服务端撤销、轮换和复用检测。 |
| Refresh Token 使用 HttpOnly、SameSite Cookie | 前端 JavaScript 不接触长期凭据，降低 XSS 后的会话窃取风险。 |
| 角色与数据范围分离 | `reporter/reviewer/auditor/admin` 表达操作能力，`own/department/descendants/global` 表达数据边界，避免角色组合爆炸。 |
| YAML 作为资产定义单一来源 | 业务字段、校验、搜索、表单、导入导出映射和存储定义可审查，避免多端手工配置漂移。 |
| 编译期生成强类型表而非通用 JSONB 主表 | 保留数据库约束、索引和查询可解释性；JSONB 只用于不可变版本快照。 |
| 服务端合并更新后再校验 | 条件必填规则可看到记录完整状态，Patch 不会绕过跨字段约束。 |
| IAM 授权上下文由资产服务逐请求获取 | 禁用和权限变化立即生效，列表、详情和写操作共享同一服务端数据范围。 |

## 已完成能力

### IAM

- 用户、部门、四类角色和四种数据范围的 PostgreSQL 模型及管理 API。
- RS256 签发和严格算法校验，issuer/audience/JTI/到期时间及权限版本校验。
- Refresh Session 持久化、原子轮换、令牌复用检测、会话族撤销和注销。
- 登录限流、未知用户等成本校验、安全 Cookie、首次登录强制改密。
- 禁用、权限变更和改密立即撤销会话；自移除管理员权限保护。

### 元数据与资产引擎

- 七份资产 YAML Schema，覆盖字段类型、必填、条件必填、枚举、范围、搜索、排序、过滤、列表、敏感字段及导入导出列元数据。
- 严格 Schema 编译器和向后兼容检查：禁止删除/改型/收窄枚举，新增必填字段必须提供默认值并提升版本。
- 生成 Go、OpenAPI、TypeScript、Vue 元数据及 PostgreSQL 强类型建表迁移。
- 七类资产统一 CRUD、查询、软删除、恢复、乐观锁和 JSONB 版本快照。
- 统一数据范围约束和敏感字段遮罩，客户端不能写入身份、部门、版本和软删除等系统字段。
- “责任部门”完成真实 PostgreSQL 创建、更新、删除、恢复和版本历史端到端验证；其导入列映射及目标表迁移由同一 Schema 生成。大文件执行器、预检确认和异步导入按计划留在阶段三。

### 前端与网关

- Pinia 内存 Access Token、HttpOnly Refresh Cookie、401 单次自动刷新。
- 首次登录强制改密页面，改密后清理会话并要求重新登录。
- 七类资产动态列表、搜索、分组表单、编辑以及管理员删除/恢复。
- 网关转发 IAM 用户/部门、授权及资产接口；公共请求体上限收紧到 1 MiB。

## 验收证据

| 验收项 | 结果 |
|---|---|
| 根模块 Go 测试 | `go test -count=1 ./...` 通过 |
| 新平台 Go 测试 | `cd platform && go test -count=1 ./...` 通过 |
| Go 静态检查 | 根模块和 `platform` 的 `go vet ./...` 通过 |
| 生成一致性 | Contract 与七类资产 Schema 的 `-check` 均通过 |
| PostgreSQL 集成 | 资产创建、更新、软删除、恢复和四个版本快照通过 |
| IAM 安全回归 | 登录/刷新/复用检测/注销/禁用/权限变化/强制改密/算法替换/限流/Cookie 测试通过 |
| Vue | 类型检查通过；Vitest 3 项通过；生产构建通过 |
| OpenAPI | Redocly 校验有效，仅保留 3 条阶段一已有的非阻塞规范警告 |
| 依赖安全 | `govulncheck` 两个 Go 模块均无可达漏洞；`npm audit` 为 0 vulnerabilities |
| Compose 全栈 | 构建和健康检查通过；改密、登录、7 类 Schema、责任部门 CRUD/版本、刷新轮换、删除/恢复、注销失效均通过 |

## 阶段边界与后续

- 阶段二生成了导入/导出字段契约和 PostgreSQL 目标结构，但没有提前实现阶段三的 RabbitMQ 任务、流式文件解析、批次事务、错误报告和异步导出。
- SQLite 历史数据迁移 CLI、dry-run、枚举清洗、对账和重复执行属于第 6 月；本阶段的“迁移”是新平台数据库 Schema 迁移。
- 当前 Compose 为本地开发可临时生成 IAM RSA 私钥。生产部署必须挂载稳定私钥并启用 Secure Cookie，私钥不得进入仓库或镜像。
- Vue 仍全量引入 Element Plus，约 1.08 MiB 的 JavaScript 包告警延续为后续性能债。

阶段三应先实现任务状态机与 Outbox/Inbox，再把当前生成的导入列元数据接入流式预检、确认提交、错误报告和幂等执行，随后开展 SQLite 到 PostgreSQL 的历史数据迁移。
