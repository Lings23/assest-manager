# 资产治理平台 Go 后端面试口述与追问手册

> 2026-10-18 性能专题更新：当前容量口径、P0–P3 实验及用户人数推算请优先阅读 [单实例并发能力与面试讲解](single-instance-concurrency-and-performance.md)。本文下方保留早期架构讲解背景；其中固定 500 行批次和旧十万行耗时不能替代最新“行数+编码字节”分批及性能验收结果。

> 使用方式：先熟悉“事实边界”和最后的“速记版”，再根据面试官关注点展开中间章节。口述时只说自己确实参与过的部分；本文依据当前仓库判断实现状态，不能代替个人提交记录或团队分工证明。

核对日期：2026-10-12。本文已同步 AMQP 与独立 worker 改造。真实 PostgreSQL/RabbitMQ 测试、标准 Compose 全链路和基础设施中断/进程强杀验收均通过；10 万行完整导入链路本机单次为19.57秒。配置、测量范围与18项证据见 [AMQP 验收记录](../architecture/amqp-worker-publisher-acceptance.md)，旧版耗时不能复用。

## 0. 先统一事实边界

这部分非常重要。业务预设和当前代码并不完全一致，面试时要主动区分“业务目标”“当前已实现”和“下一阶段规划”。

| 话题 | 当前代码的真实状态 | 面试时应如何说 |
|---|---|---|
| 系统形态 | 仓库同时保留冻结的 Go + SQLite 旧系统，以及 Go + PostgreSQL + Vue3 的新平台 | “我们采用渐进式迁移，没有一次性推翻旧系统。” |
| 服务拆分 | 新平台有 Gateway、IAM、资产、治理、任务 API、publisher、worker 七个 Go 运行进程，另有一次性迁移进程 | “已经完成基础服务边界；治理服务仍是骨架，不包装成完整治理微服务。” |
| Go Web 框架 | 新平台使用标准库 `net/http`、`http.ServeMux`、`httputil.ReverseProxy`，不是 Gin | 被问框架时直接回答 `net/http`。旧系统另有 Gin 依赖，但新平台不是 Gin。 |
| 漏洞能力 | 七类资产中有“风险漏洞清单”的通用 CRUD，包含修复建议、修复措施、完成日期 | 可以讲漏洞台账 CRUD；不能讲“整改通知、下发、状态流转”已经实现。 |
| 漏洞整改通知 | `governance-service` 只有健康检查和基础服务信息，暂无通知表、状态机、关系表和 CRUD | 只能作为明确的下一阶段设计，不写成已完成经历。 |
| 导入格式 | 新任务中心接收 JSON 中的 CSV 字符串；没有 multipart Excel 上传，也没有 `excelize` | 面试时说“当前落地是 CSV”；Excel 是业务目标或后续改造。 |
| 导入文件存储 | API 仍接收 JSON 中的 CSV 字符串，但任务服务随即写入 MinIO；任务 JSONB 只保存对象引用 | 可以说“当前 CSV 源文件先落 MinIO”；不能说已经支持 multipart Excel。 |
| 导出格式 | Worker 生成 CSV，完整内容在内存中组装后上传 MinIO | 不能说已经生成 Excel，也不能说已经流式写对象存储。 |
| MinIO | 保存导入源文件、预检错误 JSONL 和导出 CSV，并配置三类前缀的过期生命周期 | 没有预签名 URL、流式/分片上传、最小权限服务账号。 |
| RabbitMQ | 使用 AMQP 长连接订阅、manual ACK、prefetch=1、Confirm/Return | 发布与执行分进程，消息仍至少一次投递。 |
| 可靠任务 | 有 PostgreSQL Outbox/Inbox、任务状态机、重试、死信和多层幂等 | 可以讲事务提交后 ACK、租约恢复及副作用幂等；不要宣称 exactly-once。 |
| 批量入库 | Worker 每 500 行调用资产服务；资产服务单事务多值 Insert，并写幂等表和版本表 | 使用了 PostgreSQL `COPY` 写任务导入行记录；业务资产本身是多值 Insert，不是 COPY。 |
| 任务进度 | 导入进度按持有有效 token 的已提交行更新 | created/skipped 表示本次执行，committed 表示累计提交。 |
| 任务取消 | API只能取消 pending、awaiting_confirmation、failed；运行中任务会返回冲突 | 不能说长任务已经支持协作式中断。 |
| 导入部门权限 | 创建导入任务时已按 IAM AuthorizationContext 校验目标部门；内部批量接口仍使用共享服务 Token | 可以讲入口数据范围已闭环，同时承认服务间身份仍应升级为更细粒度凭据。 |
| 统计读模型 | 会按用户权限范围计算七类资产数量并写 `permission_counts` | 当前仍是请求时全量拉取再计数，不是事件驱动的高性能读模型。 |
| 服务发现/配置中心 | Compose 服务名 + 环境变量静态配置 | 没有 Consul、Nacos、etcd 或 Kubernetes 服务发现。 |
| 可观测性 | JSON 日志、请求 ID、健康检查、panic recovery | 没有 OpenTelemetry、Prometheus、集中日志平台或分布式 Trace。 |
| 数据库设计表 | `数据库设计.xlsx` 只有旧“软件信息统计”模块 35 行字段说明，使用 BIGINT/TINYINT/DATETIME 等概念 | 新平台真实 PostgreSQL 结构以迁移 SQL 和 YAML Schema 为准，Excel 不是完整新平台 ER 设计。 |
| 文档一致性 | 阶段三总结标记异步任务已完成，但 README 功能范围中仍保留“异步任务后续实现”的旧句子 | 面试以代码和阶段三验收为准，同时承认仓库文档需要继续收敛。 |

主要代码依据：`platform/cmd/`、`platform/internal/taskcenter/`、`platform/internal/asset/`、`platform/internal/iam/`、`schemas/assets/`、`compose.yaml`、`apps/web/`。

---

## 1. 简单介绍一下这个项目

### 30 秒简版

“这个项目原来是公司内部的资产信息管理系统，主要维护信息系统、软硬件、数据资产、供应链和漏洞等七类台账。随着网络安全业务增加，我们希望把它逐步融入通报整改体系，同时解决下属单位批量报送数据时同步导入导出容易超时、旧系统模块耦合比较重的问题。我的工作主要集中在新平台的 Go 后端改造，包括服务边界、Schema 驱动的资产接口、PostgreSQL 存储，以及基于任务状态机、RabbitMQ、Outbox/Inbox 和 MinIO 的异步导入导出。当前已经完成 CSV 批量任务和基础可靠性链路，漏洞整改通知工作流还属于后续阶段，我不会把它说成已经上线。”

### 2～3 分钟完整版

“这个项目最早是一个公司内部的资产信息管理系统，核心用途不是互联网流量业务，而是让不同下属单位维护信息系统、软硬件、数据资产、供应链、风险漏洞和责任部门等台账。旧系统是 Go + SQLite 加一套嵌入式前端，功能比较集中，能做基础 CRUD、CSV 导入导出和统计。

后续业务希望把资产台账逐步融入网络安全通报体系。原因是漏洞发现之后，不能只停留在一条漏洞记录，还需要后续的通知下达、责任单位、整改过程和完成状态。与此同时，下属单位数量比较多，集中报送时会出现大批量数据导入导出。如果继续在一次 HTTP 请求里完成解析、校验、写库和文件生成，请求时间会很长，失败后也不容易恢复。

所以我们没有直接重写旧系统，而是做渐进式改造。仓库中旧系统被冻结，新业务放到新平台。新平台按边界拆成 Gateway、IAM、资产服务、任务报表服务和治理服务几个 Go 进程。同步查询和鉴权走 HTTP，耗时任务通过任务中心和 RabbitMQ 解耦；PostgreSQL 负责结构化业务数据、任务状态和幂等记录；MinIO 负责保存导入源文件、错误报告和异步导出文件；Vue3 负责新的资产管理界面。

我主要参与的是 Go 后端和工程化改造：一是把七类资产做成 Schema 驱动的统一 CRUD、校验、权限过滤、软删除和版本记录；二是实现导入预检、确认提交、500 行批量入库、异步导出、任务状态、重试、死信和幂等；三是把这些服务通过 Docker Compose 和统一网关运行起来，并补充测试和验收脚本。

这里我会主动说明两个边界。第一，当前新平台实际处理的是 CSV，不是 Excel；导入源、错误报告和导出结果都放 MinIO，但 API 仍用 JSON 承载 CSV。第二，风险漏洞台账已经有 CRUD 和修复字段，但完整的整改通知、下发和状态流转还没有实现，治理服务目前仍是骨架。这些是后续继续建设的部分。”

### 面试官继续追问：最终效果是什么？

“最直接的效果有三个。第一，资产字段不再在前后端各维护一套，而是由七份 YAML Schema 生成 Go、OpenAPI、TypeScript、Vue 元数据和 PostgreSQL 迁移，减少字段漂移。第二，导入导出从同步接口变成任务模式，任务有状态、重试、死信和幂等；真实验收做过 10 万行 CSV 导入。第三，旧系统没有被强行一次性替换，冻结旧目录后可以按领域逐步迁移，风险更可控。”

---

## 2. 我在项目中主要负责哪些部分

### 先说明个人归属证据的边界

代码能证明“项目实现了什么”，但不能单独证明“某一位开发者完成了什么”。Git 历史能看到 feature/codex 分支、Pull Request 合并和 CI 规则，但第三阶段改动在当前工作区里仍有未提交文件。因此，以下口述适合在这些确实是你参与范围时使用；不要把团队成果全部说成个人独立完成。

### 最适合重点讲的 4 块工作

1. **Go 后端服务与公共工程能力**：参与 Gateway、IAM、资产服务、任务报表服务的接口和服务间 HTTP 调用；统一请求 ID、JSON 日志、错误结构、CORS、请求体限制、panic recovery、超时和优雅关闭。
2. **Schema 驱动资产引擎与 PostgreSQL**：参与七类资产 YAML Schema、服务端校验、动态但受白名单约束的 SQL、权限范围、敏感字段遮罩、软删除、乐观锁和版本快照。
3. **异步导入导出与可靠任务**：参与任务状态机、PostgreSQL Outbox/Inbox、RabbitMQ 任务队列、500 行批量入库、导入行幂等、失败重试、死信、CSV 导出和 MinIO 文件下载。
4. **工程化和验收**：参与 OpenAPI 与生成一致性、Docker 多阶段构建、Compose 环境、Go/Vue 测试、10 万行容量测试和端到端冒烟脚本。

### 可直接口述

“我在这个项目里主要负责四块。第一块是 Go 后端的服务化改造，包括网关、资产服务和任务服务之间的接口，以及统一日志、错误码、请求 ID、超时和优雅关闭。第二块是资产领域，我参与了七类资产的 Schema 驱动设计，字段校验、PostgreSQL 表、查询权限、软删除和版本控制都从同一份元数据生成或约束。第三块是批量任务，我重点做了 CSV 导入预检、确认后分批提交、RabbitMQ 任务分发、Outbox/Inbox、幂等、重试、死信和 MinIO 导出文件。第四块是工程化，包括 Docker Compose、OpenAPI 生成检查、单测和 10 万行容量验收。

漏洞整改通知这块我不会说成已经做完。当前代码实现的是漏洞台账通用 CRUD，包含修复建议、修复措施和完成日期；真正的通知下发、整改中、已整改等工作流还在治理服务的后续规划里。”

### 逐项核对

| 用户预设工作 | 代码结论 |
|---|---|
| Go 后端业务开发 | 有充分实现依据 |
| 漏洞整改通知 CRUD | **未实现**；只有漏洞资产 CRUD |
| PostgreSQL 表和 SQL | 已实现 IAM、七类资产、任务、Outbox/Inbox、幂等、版本等迁移 |
| 微服务拆分 | 已建立五进程和网关；治理服务仍是骨架 |
| 服务间调用 | IAM 授权和资产内部批量/导出使用 HTTP |
| RabbitMQ 生产消费 | 已改为 amqp091-go，手动 ACK、Confirm/Return 与租约恢复 |
| MinIO 上传下载 | 已实现导入源、错误报告和导出对象的 Put/Get、孤儿删除与生命周期 |
| Excel 导入导出 | **未实现**；当前是 CSV |
| 异步任务 | 已实现导入确认后的提交和导出任务 |
| Docker 部署 | 多阶段 Dockerfile、Compose、健康检查和数据卷已实现 |
| API/校验/错误/日志 | 已实现并有统一中间件 |
| Git 协作 | 有分支策略、PR 规则和 CI；不要仅凭仓库声称自己主导流程 |

---

## 3. 为什么从原架构拆成服务

### 1～2 分钟口述

“我们不是因为访问量大就上微服务，而是因为业务边界和资源模型开始不同。资产管理主要是稳定的 CRUD、权限过滤和查询；身份认证有独立的安全要求和会话生命周期；批量导入导出是长耗时、突发型、对 CPU、内存和数据库写入压力都比较大的任务；漏洞整改未来还会有通知、状态流转、时限和审计。这些模块如果都留在一个进程里，批量任务卡住或者内存升高会影响核心资产接口，发布一个导入功能也要带着全部系统一起发布。

所以新平台先按领域拆成 IAM、资产、任务报表和治理服务，前面放一个轻量 Gateway。同步鉴权和查询走 HTTP，耗时任务走 RabbitMQ。这样最大的收益不是所谓无限扩容，而是隔离变化和故障：任务服务可以单独调整并发和资源，资产字段变更集中在资产服务，IAM 安全策略也不会散落到每个模块。

但拆分也有成本。现在需要处理网络超时、服务间认证、接口契约、Outbox/Inbox、跨服务排错和部署配置。当前项目没有服务发现、配置中心和分布式 Trace，Compose 里还是通过固定服务名和环境变量通信。所以我把它定义为适合内部系统的基础微服务化，而不是成熟的大规模互联网微服务平台。”

### 如果继续做成单体会遇到什么

- 导入导出占用请求连接、内存和数据库连接，容易拖慢在线 CRUD。
- 资产、IAM、整改流程发布节奏不同，却必须一起构建和上线。
- 权限、任务状态和文件处理逐渐散落，代码边界越来越难维护。
- 某个批量任务异常可能扩大为整个应用不可用。

### 为什么导入导出适合解耦

导入导出是天然的“提交—等待—查询结果”模式，不要求在当前 HTTP 请求里立刻完成。把它放到任务服务后，可以排队、限制消费速度、失败重试、保存错误和独立扩容。

### 为什么漏洞整改适合独立边界

整改不是漏洞资产的几个附加字段，它通常有通知单、责任单位、状态变更、整改证据、复测、截止时间和审计记录，生命周期与漏洞台账不同。当前代码尚未实现这一服务，但从领域上独立出来是合理的。

### 微服务增加的复杂度

- 服务地址和配置管理；
- HTTP/MQ 网络失败与超时；
- 跨服务一致性与补偿；
- 日志关联和链路追踪；
- 多镜像、多容器和数据库迁移顺序；
- 消息重复、积压、死信和版本兼容；
- 测试从单进程单测扩展到 Compose 集成测试。

### 追问：你们业务量真的需要微服务吗？

“如果只是七张表的 CRUD，我也不会建议拆微服务。这个项目拆分的主要理由是边界和故障隔离，不是流量。我们也没有一开始就引入 Kubernetes、服务网格或配置中心，而是用少量 Go 进程、静态 HTTP 地址和 Compose 控制复杂度。当前治理服务还没有业务实现，也说明拆分是渐进的，不是为了凑服务数量。要是团队很小、任务量也不高，模块化单体同样是合理选择。”

---

## 4. 为什么使用 MinIO

### MinIO 在当前项目里具体做什么

“MinIO 保存导入源、错误报告和导出结果。导出使用 `exports/{taskId}/{executionToken}.csv`，旧执行者上传不会覆盖新结果，只有有效 token 的完成事务能提交 Key。任务 input 不保存 CSV 原文，但任务行仍保存规范化 payload。下载经任务服务授权与归属校验。”

需要明确：当前导入 CSV 已先存 MinIO，但还没有 Excel 或 multipart 上传。

### 为什么不把文件存 PostgreSQL

“PostgreSQL 适合保存任务状态、对象 Key、权限和结构化数据，不适合把大量导出文件当 BLOB 长期塞进去。BLOB 会放大数据库体积、WAL 和备份恢复成本，大文件读写还会占用数据库连接。对象存储可以独立扩容和清理，数据库只保存小元数据，职责更清楚。”

### 为什么不放服务器本地磁盘

“Docker 容器的可写层不是可靠持久化，容器重建后文件可能丢失；如果任务服务以后有多个实例，A 实例生成的本地文件，B 实例不一定能读取。MinIO 提供统一对象地址和持久化卷，让文件生命周期和业务进程解耦。”

### 为什么选 MinIO，不直接用公有云 OSS/S3

“这是内部网络安全场景，数据未必允许放公有云。MinIO 可以私有化部署，兼容 S3 API，Compose 本地环境也容易启动。当前规模下它的运维成本可以接受。如果以后部署环境已经有合规的云对象存储，代码的 S3 语义也方便迁移。”

### 当前 MinIO 实现不足

- 没有预签名 URL，下载必须经过任务服务转发；
- 生命周期只有统一前缀保留天数，没有对象级过期元数据和删除审计；
- 源文件记录大小和内容哈希 Key，但没有记录 ETag；
- 上传使用 `[]byte`，没有流式或分片上传；
- 使用 MinIO Root 账号，而不是最小权限服务账号。

---

## 5. 当前项目的批量导入完整流程

### 真实调用链

1. 前端或调用方请求 `POST /api/v1/import-jobs`，JSON 中携带 `asset_type`、`idempotency_key`、可选部门和完整 `csv` 字符串。
2. Gateway 把请求转发到 `task-report-service`；Gateway 和任务服务请求体上限都是 100 MiB。
3. 任务服务通过 IAM 的 `/api/v1/auth/authorize` 做 `asset:create` 授权。
4. 任务服务校验目标部门的数据范围，把 CSV 写入 `imports/`；PostgreSQL 任务 `input` 只保存对象 Key、大小和 Content-Type。导入任务此时不立即发 MQ。
5. HTTP 请求内从 MinIO 流式读取 CSV 做第一次预检：逐行检查重复/非法列、做类型转换，并复用资产服务的 `ValidateCreate`。这次 `keepRows=false`，不保留完整行对象。
6. 预检完成后，任务从 `pending → running → awaiting_confirmation`，结果保存总行数、有效行数和错误数；明细以 JSONL 写入 MinIO，并提供受保护下载接口。
7. 用户确认后调用 `POST /api/v1/import-jobs/{id}/confirm`。事务中把任务改回 `pending`，同时写 Outbox 事件。
8. 独立 publisher 每 500 毫秒扫描并短事务认领 Outbox，通过 AMQP 持久消息、mandatory、Confirm/Return 发布；消息包含 schema_version、event_id、task_id、kind、dispatch_version。
9. worker 订阅消息、检查 Inbox，再以单条条件更新原子认领任务，生成 token/租约；认领成功才增加 attempt。
10. Worker 根据任务对象 Key 从 MinIO 第二次解析 CSV，这次 `keepRows=true`，保留所有规范化行；再通过 PostgreSQL `COPY` 写入 `task_report.import_rows` 作为行级记录。
11. 每 500 行调用资产服务，整批校验并排序行键；唯一键原子占位 RETURNING 后只写获胜资产和版本，全部同事务提交。
12. 每批成功后标记任务行 `committed_at`。全部成功后，任务更新为 `succeeded`、`progress=100`，并在同一事务中写 Inbox。
13. 失败时校验 token，在同一事务写 failed/下一代 Outbox 或死信/dead Outbox 和 Inbox；提交后 ACK 原消息。最多三次业务执行，publisher 负责所有发送。

### 可直接口述

“当前落地不是上传 Excel 文件，而是把 CSV 作为 JSON 字符串提交。任务服务先校验权限并把源文件放入 MinIO，再从对象存储流式预检；有错误时把逐行明细写成 MinIO JSONL，任务结果只保存数量和对象引用。预检通过以后任务进入待确认状态，用户确认时在同一个数据库事务里把任务改回 pending 并写 Outbox。

独立 publisher 把 Outbox 小消息发布到 RabbitMQ，包含任务标识、事件 ID、格式版本和调度代次，不传文件内容。消费后 Worker 会重新解析 CSV，把行记录通过 PostgreSQL COPY 写入任务表，再每 500 行调用资产服务。资产服务一批使用一个事务，业务数据、行级幂等键和版本快照一起提交。失败会重试，超过 3 次进入死信。

这套设计解决了任务 JSONB 大字段、错误列表膨胀、确认后批量写库和重复消费的问题，但 Worker 第二次解析仍会把规范化后的全部行保存在内存里，所以还不是理想的真正流式 Excel 导入。”

### 为什么不直接在 HTTP 请求里全部处理

- 10 万行解析、校验和写库时间不可控，容易超过反向代理或客户端超时；
- 长请求占用连接和服务资源，用户也无法判断进度；
- 写到一半失败后，同步接口很难提供重试和恢复；
- 多单位同时提交时，需要队列控制处理节奏。

当前代码仍在 HTTP 中做第一次预检，这是为了让用户确认前快速看到校验结果；真正入库在 Worker 中完成。

### RabbitMQ 消息里放什么

只放通知元数据，不放 CSV：schema_version、event_id、task_id、kind、dispatch_version；MessageId 与 event_id 相同，兼容期继续携带已有 Header。MQ 负责“通知有任务可处理”，任务详情从 PostgreSQL读取。这样避免大消息占用 RabbitMQ 内存、磁盘和网络，也避免消息大小与文件大小绑定。

### 大批量写入做了哪些优化

- 500 行分批，限制单事务大小；
- 业务资产使用一条多值 `INSERT ... VALUES (...)`；
- 导入行记录使用 `pgx.CopyFrom`；
- 每批业务数据、幂等记录、版本记录在同一事务提交；
- 行键排序、唯一键原子占位和执行 token/租约防止重复写入；
- 复用 Schema 校验，避免导入绕过正常业务规则。

进一步可优化：改成客户端直传/分片上传，用临时表 + COPY + set-based merge、限制 Worker 并发、分批更新真实进度，并为对象访问增加审计与配额。

---

## 6. 当前项目的批量导出完整流程

### 真实调用链

1. 用户请求 `POST /api/v1/export-jobs`，提交资产类型和幂等键。
2. 任务服务做 `asset:read` 授权，把用户 ID、部门范围、全局范围和敏感字段查看权限固化到任务 `input`。
3. 创建任务和 Outbox 事件使用同一个 PostgreSQL 事务。
4. publisher 发布任务，worker 订阅并以数据库原子认领取得执行权。
5. Worker 调用资产服务内部 `export-page` 接口，每页最多 5000 行，使用 Offset 分页并带权限条件。
6. 当前实现会把所有页合并成 `[]Record`，再把完整 CSV 写入 `bytes.Buffer`，不是边查边写。
7. Worker 将 `exports/{taskId}/{executionToken}.csv` 上传 MinIO。
8. 有效 token 的完成事务保存 rows、object_key、download_url 和 Inbox，任务变为 succeeded，之后 ACK。
9. 前端目前没有任务页面，但调用方可轮询 `GET /api/v1/tasks/{id}` 或导出任务列表判断完成。
10. 下载时请求 `GET /api/v1/export-jobs/{id}/download`。服务验证任务属于当前用户且成功，然后从 MinIO读取并通过 `io.Copy` 返回。

### 可直接口述

“导出也做成任务，是因为几十万行查询和文件生成不适合占着 HTTP 连接。创建任务时我们会把当时的权限范围固化到任务输入，并在同一事务写 Outbox。Worker 收到任务后，通过资产服务内部接口每 5000 行查询，生成 CSV 后上传 MinIO。任务表只保存对象 Key、行数和下载接口。用户轮询任务状态，成功后再通过受保护的下载接口取文件。

当前下载权限比较明确：只能下载自己创建并且已经成功的导出任务。失败通过事务 Outbox 重试或死信。当前仍把全部记录和 CSV 放在内存，并使用 Offset 深分页；文件按 exports/ 生命周期过期，但尚无对象级过期元数据。”

### 为什么导出也需要 RabbitMQ

导出同样是长耗时、可排队、可重试的工作。多个单位同时导出时，RabbitMQ 可以把请求快速变成任务，由 Worker 按能力消费；资产在线查询接口不必承担文件生成和对象存储上传。

---

## 7. 为什么使用 RabbitMQ

### 1～2 分钟口述

“项目里 RabbitMQ 的主要价值不是泛泛的高并发，而是把耗时任务从 HTTP 请求中拿出来。API 服务只负责鉴权、创建任务和返回 taskId，Worker 负责真正的批量写库或导出文件。多个下属单位集中操作时，任务先进入队列，Worker 可以按处理能力消费，这相当于给 PostgreSQL 和文件处理做了一层缓冲。

我们选择 RabbitMQ，是因为这里是典型任务队列，需要路由、持久消息、失败重试和死信，但不是海量日志流。Kafka 在吞吐和事件回放方面更强，但对这个内部系统偏重；Redis List 太简单，确认和死信要自己补；Redis Stream 能做消费组，但项目已经有 RabbitMQ 运维基础时收益不明显；Go Channel 只能在一个进程内使用，进程重启消息就没了，也不能跨服务。

当前使用 durable direct exchange/queue、持久消息、Outbox/Inbox、重试和死信；AMQP 手动 ACK、prefetch、Confirm/Return 与执行租约已经实现。真实故障测试及新版容量结论以验收记录为准，单节点部署仍不是生产高可用。”

### 简单对比

| 方案 | 为什么不是当前首选 |
|---|---|
| Kafka | 更适合高吞吐事件流、日志和长期回放；本项目任务量和路由模型不需要它的复杂度 |
| Redis Stream | 能做消费组和 Pending，但仍要补完善的重试、死信和运维规范 |
| Redis List | 结构简单，但可靠确认、消费组、重试和死信能力弱 |
| Go Channel | 仅进程内；无法跨服务、无法持久化、进程退出即丢失 |

---

## 8. RabbitMQ 可靠性真实实现

### 已实现

- API、publisher、worker 独立进程；运行时只用 RABBITMQ_URL 连接 AMQP。
- durable exchange/queue 与持久消息；mandatory + Confirm/Return，确认成功且没有 Return 才回写 Outbox。
- Outbox 通过 SKIP LOCKED 短事务认领，claim token 与租约保护晚到回写；网络发送不持有事务。
- worker 默认两个 slot，独立订阅，手动 ACK、prefetch=1；数据库原子认领决定同任务有效执行者。
- 调度代次、next_run_at、worker ID、执行 token 与租约保护进度/完成/重试；只成功认领才增加 attempt。
- 成功、可靠重试或死信与 Inbox 同事务提交，之后 ACK；非法消息先持久化隔离和 dead Outbox。
- 独立恢复循环回收过期租约；预检代次 0 不会被异步调度。
- 导入行唯一键幂等，导出按 token 隔离；SIGTERM 停接新任务并等待有界 drain。

### 故障与边界

“Consumer 中途退出，原 channel 的未确认消息由 Broker 重投；任务若已被认领，则租约到期后由恢复器生成下一代事件。成功提交但 ACK 丢失时，Inbox 与任务终态消重。数据库提交结果不明时，不贸然把任务改失败，而是关闭 channel 并核实状态。”

至少一次投递可能重复，不能声称恰好一次。租约切换时旧进程已发出的 HTTP/上传仍可能完成，因此资产幂等和 token 对象路径是必要的副作用保护。当前部署保留单节点和现有队列类型，没有 quorum queue/集群高可用；故障与容量结论必须引用本次实际测试。

---
## 9. 导入任务如何保证幂等

### 四层幂等

1. **API 任务层**：`UNIQUE(requested_by, kind, idempotency_key)`，同一用户重复提交得到原任务。
2. **消息层**：Outbox `event_id` 唯一；Inbox 主键是 `(consumer,event_id)`。
3. **导入行层**：`task_report.import_rows` 使用 `(task_id,row_number)` 主键和 `(task_id,row_key)` 唯一约束。
4. **资产写入层**：asset.import_idempotency 主键 (task_id,row_key) 原子占位，按行键固定排序；只为获胜行写资产和版本，三者同事务提交。

### 可直接口述

“RabbitMQ 通常按 At-Least-Once 来设计，所以我们不假设消息只来一次。用户创建任务时必须提供 idempotencyKey，数据库按用户、任务类型和这个 Key 做唯一约束。Worker 处理事件前检查 Inbox；真正入库时，每行用 `taskId:rowNumber` 作为 rowKey。资产服务同事务批量 INSERT ON CONFLICT DO NOTHING RETURNING 占位，再插获胜资产和版本。

如果某一批资产已经提交，但任务服务在标记完成前失败，重试同一批时会命中 `asset.import_idempotency`，已经写过的行只计为 skipped，不会再创建。这样幂等落在数据库约束上，比在内存里维护一个 set 更可靠。”

### 仍需注意的边界

- 相同业务数据换一个新 taskId 仍可能重复，因为没有为所有资产定义统一业务唯一键；
- 成功、已安排重试或死信均同事务记录 Inbox；长任务中途恢复仍依赖行级幂等；
- 数据库事务提交后 ACK；ACK 失败仍可能重投，因此不能假设消息只来一次；
- 独立恢复器回收过期租约，旧执行者所有写入都需验证 token 与租约；
- RecordImportRows 在有效执行权事务中 COPY 到 staging 再合并，保留 committed_at；仍会重读和重放整个文件。

---

## 10. 漏洞整改通知 CRUD：当前实现与正确说法

### 当前已实现的漏洞资产

`vulnerability_assets` 属于七类资产之一，复用统一资产 Handler → Service → Store 结构，已有：

- 通用列表、新增、详情、更新、软删除、恢复和版本查询 API；
- 字段包括系统名称、发现日期/方式、受影响设备、漏洞名称、CVE/CNVD、IP、严重程度、风险描述、风险影响、修复建议、修复措施、完成日期；
- owner、department、version、created/updated/deleted 和资产版本快照；
- 权限范围和敏感字段策略。

### 当前没有实现的整改通知

- 没有 `remediation_notices` 或整改任务表；
- 没有待下发、已下发、整改中、待复测、已整改、逾期等状态机；
- 没有通知与漏洞、资产、单位的关系表；
- 没有下发人、整改责任人、截止时间、证据附件、复测结论和操作日志；
- 没有通知 CRUD API；
- `governance-service` 只有基础健康/身份接口；OpenAPI 的 workflows/risks 路径没有真实业务 Handler。

### 面试时可直接说

“业务上确实希望增加漏洞整改通知，但当前仓库完成的是漏洞台账，不是完整整改闭环。漏洞台账已经有统一 CRUD，包含修复建议、修复措施和完成日期。完整通知模块应该放在治理服务里，我参与的是前置的服务边界和资产/任务基础设施；如果被问到通知下发状态机，我会作为下一阶段设计讲，不会说已经上线。”

### 如果面试官让你现场设计

建议核心表：

- `remediation_notices`：通知编号、标题、状态、下发单位/人员、截止时间、创建人、版本；
- `notice_vulnerabilities`：通知与漏洞多对多关联；
- `notice_assets`：通知与受影响资产关联；
- `remediation_actions`：整改措施、提交人、提交时间、说明；
- `remediation_evidence`：MinIO Object Key、文件名、哈希、大小；
- `remediation_history`：前后状态、操作人、时间、意见、request_id。

状态建议：`draft → issued → remediating → pending_review → remediated`，并允许 `rejected → remediating`、`issued/remediating → overdue`。状态变更和 Outbox 事件同事务提交，通知消息异步发送，不能把外部通知结果和本地数据库更新绑在一个分布式事务里。

---

## 11. 结合当前项目的 30 个 Go 后端面试问题

## 初级：基础语言与项目结构

### 1. 新平台为什么使用 `net/http`，而不是 Gin？

“新平台路由和中间件需求相对清晰，Go 新版 `http.ServeMux` 已经支持方法和路径参数，所以直接用标准库可以减少框架依赖。Gateway 也直接使用 `httputil.ReverseProxy`。统一请求 ID、日志、CORS、Body Limit、Recovery 都是普通 `http.Handler` 组合。旧系统和新平台技术选择不同，回答时我会明确新平台是 `net/http`。”

追问时可补充：标准库不等于没有分层，资产模块仍然是 HTTP Handler → Service → Repository/Store。

### 2. Goroutine 和线程有什么区别？项目哪里用了 Goroutine？

“Goroutine 是 Go runtime 调度的轻量执行单元，栈可以动态增长，大量 Goroutine 会复用较少的操作系统线程。项目里每个 HTTP 请求由 `net/http` 在 Goroutine 中处理；服务启动时还会单独 `go worker.Run(ctx)` 跑任务循环，HTTP Server 也在一个 Goroutine 中启动。但轻量不代表免费，文件解析、内存和数据库连接仍然要限流。”

### 3. Channel 在项目中怎么用？能不能替代 RabbitMQ？

“公共 Server 用一个容量为 1 的 `errCh` 把 `ListenAndServe` 结果传回主 Goroutine。Channel 适合进程内协作，但不能替代 RabbitMQ：Channel 不跨进程、不持久化，服务重启后任务会丢。RabbitMQ 用于任务服务内部 Worker 与持久队列之间的跨进程可靠通信。”

### 4. Buffered Channel 和 Unbuffered Channel 有什么区别？

“无缓冲 Channel 的发送和接收必须同时就绪，适合强同步；有缓冲 Channel 在缓冲区未满前可以先发送。项目的 `errCh` 容量为 1，是为了即使主 Goroutine 还在 select 初始化阶段，Server Goroutine 也能报告一次错误而不被卡住。如果设计导入 Worker Pool，我会用有限缓冲做背压，而不是无限堆任务。”

### 5. `defer` 的执行顺序和项目用法？

“同一个函数的 defer 后进先出。项目里数据库事务开始后立即 `defer tx.Rollback(ctx)`，成功路径再显式 Commit；Commit 后 deferred Rollback 不会破坏结果。HTTP Response Body、rows、ticker、数据库连接也都用 defer 关闭。要注意循环里直接 defer 可能积累资源，批量处理更适合封装成小函数。”

### 6. Slice 扩容机制对批量任务有什么影响？

“Slice append 容量不够时会重新分配并复制，具体增长比例属于运行时实现细节，不能依赖固定倍数。项目批量代码会按已知大小 `make(..., 0, len(rows))`，减少扩容。当前导出把所有记录 append 到一个大 Slice，再生成完整 Buffer，几十万行时会造成两份以上内存占用，这是需要改成流式导出的原因。”

### 7. Go map 是否线程安全？项目怎么避免问题？

“普通 map 并发读写不安全。worker 多个 slot 的执行局部 map 由当前执行持有，不共享业务幂等状态；幂等和执行权落 PostgreSQL。AMQP 共享连接状态用锁保护；新增进程内缓存仍需 Mutex 或 sync.Map，并通过竞态测试验证。”

### 8. Interface 在项目里解决了什么问题？

“任务中心把 Broker、ObjectStore、AssetClient 都定义成小接口，Worker 不依赖具体 RabbitMQ、MinIO 或 HTTP 实现，测试时可以替换为 stub。资产 Service 依赖 Repository 和 Authorizer 接口，也方便做权限和业务单测。我的原则是由使用方定义最小接口，不为了抽象而设计很大的通用接口。”

### 9. `context.Context` 在项目里起什么作用？

“Context 贯穿 HTTP、IAM 授权、PostgreSQL、RabbitMQ HTTP 和 MinIO 请求。客户端断开或服务收到 SIGTERM 时，取消信号能传到下游。服务启动使用 `signal.NotifyContext`，关闭时再用带 10 秒超时的新 Context 调 `Server.Shutdown`。Context 只传截止时间、取消和请求级值，不用来塞业务参数。”

追问 `select` 时可以说：`select` 用来等待多个 Channel 事件，某一分支就绪就执行；没有就绪分支时会阻塞，有 `default` 时则不阻塞。项目 Server 用它在“监听失败”和“系统取消”之间选择，Worker 用它在 ticker 和 `ctx.Done()` 之间选择。

### 10. `panic` 和 `error` 应该怎么选择？

“可预期的业务问题，例如参数非法、版本冲突、资源不存在，都返回 error 并映射成 400、404、409。panic 只代表程序不变量被破坏或无法合理恢复的错误。公共中间件有 recovery，会记录 request_id 和 stack，再返回统一 500，但不能用 recovery 替代正常错误处理。”

## 中级：并发、Web 与 PostgreSQL

### 11. 解析 10 万行能不能直接开 10 万个 Goroutine？

“不能。每行都开 Goroutine 会带来调度、栈、对象和数据库请求压力，还会把错误收集和事务控制搞复杂。当前代码是流式顺序预检，入库按 500 行批次。若某些 CPU 型校验确实可并行，我会使用固定大小 Worker Pool，大小结合 CPU、数据库连接池和下游限额压测确定。”

### 12. 如果要设计导入 Worker Pool，会怎么做？

“Reader Goroutine 顺序读取并给每行编号，把任务送入有限缓冲 Channel；固定数量校验 Worker 处理纯计算；Aggregator 按行号收集结果或送批次 Writer。使用 `errgroup.WithContext`，任何不可恢复错误会 cancel 全链路。数据库写入并发要比校验低，通常由少量 Writer 按批提交，避免把连接池打满。”

### 13. 怎么避免 Goroutine 泄漏？

“每个阻塞发送、接收和外部调用都要能监听 `ctx.Done()`；生产者退出时明确关闭由它拥有的 Channel；消费者不关闭输入 Channel；HTTP、数据库和对象存储都设置超时。worker 停止新订阅后保持执行和续租，等待有界 drain；超时再取消并关闭原消费 channel，最后等待执行与恢复 goroutine 退出，再关闭数据库。”

### 14. 中间件是怎么实现的？顺序有什么影响？

“公共 `NewHandler` 用 Handler 嵌套组合：Request ID、CORS、安全 Header、Body Limit、日志、Recovery、最终路由。顺序会影响日志能否拿到 request_id、CORS 拒绝是否被记录以及 panic 是否被捕获。Gateway 清理转发请求的 Origin，让内部服务不重复处理浏览器 CORS。”

### 15. JWT 和登录认证怎么做？

“IAM 使用 RS256 Access Token，包含 issuer、audience、JTI、过期时间和 permissions_version，Access Token 固定 15 分钟。Refresh Token 只通过 HttpOnly Cookie 暴露给浏览器，数据库保存哈希；刷新时原子轮换，如果旧 Refresh Token 被重复使用，会撤销整个 session family。Vue 的 Access Token 只存在 Pinia 内存，不写 localStorage。资产服务每次通过 IAM 内部授权接口验证用户状态和数据范围。”

### 16. 参数校验和错误码怎么设计？

“JSON Decoder 使用 `DisallowUnknownFields`，并要求 Body 只有一个 JSON 值；公共层限制 Body 大小。资产字段由生成的 Schema 校验必填、类型、枚举、长度、范围和条件规则，未知字段不能进入 SQL。错误统一为 `error.code/message/request_id`，认证、权限、资源不存在、版本冲突分别映射到 401、403、404、409。”

### 17. HTTP 超时和优雅关闭怎么处理？

“API 和下游调用有各自超时。worker 收到 SIGTERM 后先变为 not-ready、停止新订阅，保持执行 context 和续租，最多等待 60 秒 drain；超时再取消执行、关闭消费 channel 并等待 goroutine 退出，最后关数据库。Compose 给 75 秒宽限，任务上限 20 分钟与 Broker 确认超时 30 分钟分别配置。”

### 18. PostgreSQL 为什么适合这个项目？

“资产字段比较结构化，需要事务、约束、索引和 JSONB 版本快照；任务可靠性又需要 Outbox、Inbox、唯一约束和行锁语义。PostgreSQL 同时支持强类型表、数组、JSONB、COPY 和丰富索引，比较适合内部治理系统。项目没有把所有业务字段塞 JSONB，七类资产是强类型表，JSONB主要保存不可变版本和任务输入结果。”

### 19. 项目事务怎么使用？隔离级别是什么？

“代码使用 pgx 默认事务隔离，通常为 PostgreSQL Read Committed。任务与 Outbox 同事务；每批资产、幂等占位和版本同事务。执行权用条件 UPDATE RETURNING 和 token/租约校验，副作用用唯一键原子占位，不需要 advisory lock。涉及多表的执行写入先锁任务并在同一短事务验证执行权，网络调用不持有事务。”

### 20. 索引和慢查询怎么分析？

“生成迁移为每张资产表建了 `(deleted_at, updated_at DESC)` 活跃数据索引，并为 Schema 标为 searchable/filterable 的字段建索引；任务表有状态和创建时间索引，Outbox 有未发布事件部分索引。出现慢查询时先结合结构化日志定位接口，再用 `EXPLAIN (ANALYZE, BUFFERS)` 看扫描方式、估算偏差和 IO。不能看到过滤字段就盲目建索引，还要考虑选择性和写放大。”

### 21. Offset 分页有什么问题？项目怎么改？

“Offset 越深，数据库需要跳过的行越多；并发新增还可能导致重复或漏数据。当前公共列表和内部导出都用 Offset，内部页大小提高到 5000 只是缓解，不是根治。大导出应按稳定排序键做 Keyset，例如 `(updated_at,id) > (?,?)`；或者用只读事务游标流式读取，并控制快照和超时。”

### 22. PostgreSQL COPY 适合什么场景？项目哪里用了？

“COPY 适合列固定、数据量大的批量写入。当前 `RecordImportRows` 使用 `pgx.CopyFrom` 写任务中心的行记录；真正的七类资产因为表和字段随 Schema 不同，还要同时写幂等和版本表，所以使用每 500 行多值 Insert。进一步优化可以先 COPY 到按任务隔离的 staging 表，再用 SQL 校验和 `INSERT ... SELECT` 合并到目标表。”

## 深挖：RabbitMQ、MinIO、微服务和 Docker

### 23. RabbitMQ 的 Exchange、Queue 和 Binding 是什么关系？

“Producer 向 Exchange 发布，Exchange 根据类型、routing key 和 binding 把消息路由到 Queue，Consumer 从 Queue 消费。项目用 direct exchange，`task.requested` 绑定主队列，`task.dead` 绑定死信队列。Direct 适合明确任务路由；Fanout 是广播；Topic 支持通配符，适合多类领域事件订阅。”

### 24. ACK、NACK 和 Prefetch 分别解决什么？当前实现怎样？

“ACK 确认当前 delivery，NACK 可重投或走 DLX，prefetch 限制未确认数量。当前每个 slot 手动 ACK、prefetch=1，只在结果或可靠重试事务提交后单条 ACK，并绑定原 channel。数据库异常或提交不明时关闭 channel、退避重连，业务重试通过 Outbox，避免即时 NACK 重投忙循环。”

### 25. Outbox/Inbox 为什么还需要数据库唯一约束？

“Outbox 解决数据库提交成功但消息没发出去的问题，Inbox 记录事件是否处理过。但发布和消费仍可能重复，Inbox 也可能在业务处理后写入失败，所以最终业务写入必须自己幂等。项目通过任务唯一键、Event ID、Inbox、执行 token/租约、行级 Key 与资产幂等表形成多层防线。”

### 26. 消息积压怎么处理？

“先看是生产突增、Consumer 变慢还是下游 PostgreSQL/MinIO故障。当前项目缺少队列深度指标和自动扩容，我会先增加 RabbitMQ queue depth、任务耗时、失败率和 oldest message age 告警，再根据数据库容量增加 Worker。不能只盲目加 Consumer，否则可能把积压从 RabbitMQ 转移成数据库连接耗尽。”

### 27. MinIO 的 Bucket 和 Object Key 是什么？下载权限怎么做？

“Bucket 是对象命名空间，Object Key 是 bucket 内唯一路径。项目 bucket 默认是 `asset-exports`，按 `imports/`、`import-errors/`、`exports/` 区分源文件、错误报告和导出结果。下载不暴露 MinIO 地址，任务服务先验证 Access Token 和任务所有者，再用服务账号读取对象并 `io.Copy` 返回。这是应用层授权，不是预签名 URL。”

对象存储和普通文件系统的区别是：对象通过 Key 整体读写，通常不提供任意位置修改、目录锁和 POSIX 语义；所谓目录只是 Key 前缀。它更适合导出包、附件和归档，而不是数据库数据文件。

### 28. Presigned URL 和生命周期怎么设计？

“Presigned URL 是服务端对某个对象和短有效期生成带签名的临时访问地址，可以减少大文件经过业务服务转发。当前项目没有实现。我会在任务结果里增加 `expires_at`，MinIO配置生命周期自动删除，例如保留 7 天；下载时动态生成 5～10 分钟 URL，同时保留权限检查和审计。敏感内网环境也可以继续由网关代理，但要做流式传输和限速。”

### 29. 当前项目算真正的微服务吗？

“它具备独立进程、独立领域目录、Schema 所有权、HTTP/MQ通信和独立镜像这些基础特征，但成熟度还比较早。所有服务共享一个 PostgreSQL 实例，只是不同 Schema；服务地址靠 Compose DNS 和环境变量；治理服务还是骨架；没有服务发现、配置中心、Trace、熔断和独立发布流水线。所以我会说它完成了基础微服务化或服务化拆分，不会包装成成熟的大规模微服务平台。”

### 30. Go 服务镜像怎么构建？Compose 解决什么？

“Go Dockerfile 是多阶段构建：第一阶段用固定摘要的 Go Alpine 镜像下载依赖并 `CGO_ENABLED=0` 编译单个 `cmd/{SERVICE}`；第二阶段只复制二进制到 Alpine，并以非 root app 用户运行。Vue 也是 Node 构建、Nginx 非 root 运行。Compose 把 PostgreSQL、RabbitMQ、MinIO、七个 Go 运行进程、一次性迁移和 Web 放进同一网络，通过服务名通信，并配置健康检查、依赖顺序、端口和持久卷。容器删除后命名 volume 还在，所以数据库和对象数据不会跟容器一起丢；只有 `down -v` 才会删除这些卷。”

追问时补充：`ENTRYPOINT` 定义容器的主要可执行程序，`CMD` 常用于默认参数，两者组合时运行参数可以覆盖 CMD；本项目 Go 镜像使用固定 ENTRYPOINT。容器共享宿主机内核、隔离进程和文件系统，通常比包含完整 Guest OS 的虚拟机更轻，但隔离边界也不同。

---

## 12. 五组连续技术追问

## 追问链一：RabbitMQ 可靠性

**面试官：你们为什么用 RabbitMQ？**

“导入导出是长任务，API 只创建任务，Worker 异步处理；队列还能让多个单位集中提交时不会同时冲击数据库。”

**面试官：为什么不用 Kafka？**

“我们不是海量日志流，也没有长期回放需求，重点是任务路由、失败重试和死信。RabbitMQ更贴合，运维成本也更低。”

**面试官：RabbitMQ 挂了怎么办？**

“任务和 Outbox 已经在 PostgreSQL 提交，发布失败会记录错误并指数退避，Rabbit 恢复后继续发布。不过当前 Compose 是单节点，没有 quorum queue 或集群高可用，生产还要补。”

**面试官：Producer 怎么知道消息真的到了？**

“publisher 开启 Confirm 并 mandatory 发布，同时监听 Return、Confirm 和连接关闭。单条在途时按 MessageId/发送序号关联，只有 Confirm ACK 且未 Return 才标记已发送；确认后回写前崩溃允许同 event ID 重发。不可路由的 ACK+Return 不能算成功，需真实 Broker 验证。”

**面试官：Consumer 处理中挂了呢？**

“原 HTTP 实现有提前 ACK 窗口，现已改为 AMQP 手动 ACK。结果或可靠重试提交后才 ACK；进程崩溃后 Broker 重投，过期运行任务由独立租约恢复器安排下一代。”

**面试官：重复投递怎么办？**

“不追求 MQ Exactly-Once，而是通过任务唯一键、Inbox 和 `(task_id,row_key)` 数据库幂等让重复投递无副作用。”

## 追问链二：10 万行导入

**面试官：10 万行为什么不在 HTTP 里直接导？**

“解析、校验、写库时间长，长连接容易超时；失败难恢复，也会占用在线服务资源。HTTP 只做预检和创建任务，确认后异步写库。”

**面试官：你说流式，真的完全不占内存吗？**

“第一次预检是逐行读取且不保留行，但 Worker 第二次解析使用 `keepRows=true`，会保存全部规范化行。所以当前只能说预检流式，不是全链路流式。”

**面试官：为什么还解析两次？**

“第一次为了尽快把错误反馈给用户并进入确认状态，第二次在确认后生成真正提交的数据。更好的方式是预检时把规范化结果写 staging 或 MinIO，确认后直接流式读取，避免重复解析。”

**面试官：一批为什么是 500 行？**

“这是事务大小、SQL 参数数、网络往返和失败重试粒度之间的折中，并通过 10 万行验收验证过。它不是永远正确的常数，生产要根据行宽和数据库压测调整。”

**面试官：写到一半挂了怎么办？**

“已提交批次不会回滚，但每行有 taskId + rowKey 幂等记录。任务重试时已提交行会 skipped，未提交批次继续写。这样避免一个覆盖 10 万行的超大事务。”

**面试官：会不会出现半成功？**

“任务层面可能在中途失败，但数据层面每个批次原子提交。最终要靠重试收敛；如果决定人工终止，需要把已提交数量和失败原因展示清楚，而不是声称全任务原子。”

**面试官：怎么再优化？**

“文件先入 MinIO，流式解析到 staging 表，COPY 批量装载，SQL set-based 校验和 merge；增加真实进度、错误文件、租约恢复和并发限流。”

## 追问链三：异步导出与 MinIO

**面试官：为什么导出也异步？**

“几十万行查询、序列化和上传时间长，直接 HTTP 返回容易超时，也无法排队和重试。”

**面试官：导出权限怎么保证？**

“创建时把用户的数据范围和敏感字段权限固化到任务输入；下载时还要求任务属于当前用户并且成功。”

**面试官：数据权限变化后，旧任务怎么办？**

“当前导出使用创建时快照，下载只复核身份和所有者，没有再次核对权限版本。这是需要明确策略的地方：高敏场景应在下载时重新授权或让任务绑定 permissions_version，变化后使旧文件失效。”

**面试官：几十万行内存会不会爆？**

“当前会，因为所有记录先进入 Slice，CSV 又进入 Buffer。应该用 Keyset/游标读取，`io.Pipe` 或临时文件流式编码，再用 S3 multipart upload，控制固定内存。”

**面试官：为什么不放数据库或本地磁盘？**

“数据库 BLOB 会拖累 WAL 和备份；容器本地磁盘不可靠也不共享。MinIO提供独立、持久、可多实例访问的对象存储。”

**面试官：文件什么时候删除？**

“当前已按 `imports/`、`import-errors/`、`exports/` 配置统一保留天数，并在部分失败路径主动删除孤儿对象。生产还应增加任务元数据过期时间、删除审计、配额告警和最小权限服务账号。”

## 追问链四：微服务与一致性

**面试官：为什么不做模块化单体？**

“如果只有 CRUD，模块化单体足够。我们拆分是因为 IAM 安全、在线资产和批量任务的资源及发布周期不同，但仍保持服务数量有限。”

**面试官：服务之间怎么通信？**

“即时授权和资产批量接口走 HTTP；耗时任务通知走 RabbitMQ；文件走 MinIO；状态和幂等落 PostgreSQL。”

**面试官：为什么鉴权必须同步 HTTP？**

“用户禁用、角色变化和权限版本需要立即生效，所以资产服务逐请求询问 IAM。代价是 IAM 可用性影响资产接口，后续可以做短 TTL 缓存和熔断，但不能无限缓存旧权限。”

**面试官：跨服务事务怎么办？**

“不做 XA。任务服务用本地事务写任务和 Outbox；资产服务每批用自己的本地事务写资产和幂等。跨服务通过消息重试、幂等和状态补偿实现最终一致。”

**面试官：资产写成功、任务更新失败怎么办？**

“重试同一 taskId/rowKey，资产服务命中幂等表后跳过，任务再收敛到成功。”

**面试官：怎么排查一次跨服务失败？**

“当前主要靠统一 request_id 和 JSON 日志，但 MQ 消息没有完整 Trace Context，也没有集中 Trace。这是后续要接 OpenTelemetry 的原因。”

## 追问链五：漏洞整改通知设计

**面试官：你们整改通知 CRUD 怎么做的？**

“当前没有完整实现。我负责或参与的是漏洞台账和治理服务基础边界，完整通知工作流是下一阶段，不能把规划说成上线功能。”

**面试官：那当前漏洞模块有什么？**

“有漏洞资产的通用 CRUD、权限、软删除、版本记录，以及修复建议、修复措施和完成日期字段。”

**面试官：如果让你实现，核心表怎么拆？**

“通知主表、通知与漏洞/资产关联表、整改动作、证据对象、状态历史。附件只存 MinIO Key 和元数据。”

**面试官：状态怎么防止乱跳？**

“在 Service 层定义显式状态转移表，UPDATE 时把旧状态和 version 放进 WHERE，实现状态校验和乐观锁；状态变化和 Outbox 同事务。”

**面试官：通知下发失败怎么办？**

“本地通知单先提交，再通过 Outbox 异步发送；失败重试并记录发送状态，不让外部通知系统和本地事务形成长事务。”

**面试官：整改证据怎么保存？**

“文件放 MinIO，数据库存 Object Key、原始文件名、大小、哈希、上传人和时间；下载要校验通知和单位权限。”

**面试官：怎么处理逾期？**

“定时扫描截止时间或使用延迟任务生成 overdue 事件；状态变更必须幂等，并保留操作历史和提醒次数。”

---

## 13. 最容易被面试官质疑的地方

### 1. 可以放心写进简历

- Go `net/http` 服务工程基线：统一中间件、Gateway、服务启动和优雅关闭；
- PostgreSQL 的 Schema 驱动七类资产、强类型表、参数化 SQL、软删除、乐观锁和版本快照；
- IAM 的 RS256、Access/Refresh 分离、Refresh 轮换与复用检测、角色和部门数据范围；
- 任务状态机、事务 Outbox/Inbox、任务/行级幂等、批次事务、重试和死信的基础实现；
- CSV 流式预检、500 行批量写入、10 万行真实容量验收；
- MinIO导出对象上传和受保护下载；
- Docker 多阶段构建、非 root 容器、Compose 健康检查和持久卷；
- OpenAPI/Schema 生成一致性和 Go/Vue CI。

建议简历措辞：“参与设计并实现”“负责其中的……链路”，不要写“从零主导整个微服务平台”，除非你的提交和实际团队职责能证明。

### 2. 可以说参与，但不建议说主导

- 整体微服务架构：服务边界已经建立，但治理服务和生产治理能力不完整；
- RabbitMQ 可靠消息：已实现 AMQP 手动 ACK、Confirm/Return、Outbox 认领和执行租约；仍需区分真实故障测试与生产高可用；
- 高性能批量导入导出：10 万行导入通过，但全链路仍有内存和深分页问题；
- DevOps/生产部署：Compose 和 CI 完整度不错，但没有 Kubernetes、灰度、监控告警和真实生产高可用证据；
- Git 协作流程：仓库有分支/PR/CI 规范，但个人主导程度必须用真实团队经历说明。

### 3. 代码里实际上没有实现

- Excel/xlsx 解析和生成；
- 客户端直传或 multipart 上传；
- 漏洞整改通知 CRUD、下发、状态跟踪和复测闭环；
- RabbitMQ 集群、quorum queue 和跨节点高可用；
- MinIO Presigned URL、对象级删除审计与配额；
- 服务发现、配置中心、熔断、限流平台；
- OpenTelemetry、Prometheus、Grafana 和集中日志；
- Kubernetes、自动扩缩容和多环境发布；
- 导入/导出任务的 Vue 页面和进度轮询界面；
- 全量审计事件和不可篡改审计链。

此外，虽然对外有取消 API，但当前运行中的任务不能取消；三个任务进程的 readiness 已探测各自必要依赖；其他领域的健康实现仍需按实际代码区分。

### 4. 哪些微服务能力只是基础版本

- 运行进程共享一个 PostgreSQL 实例，只按 Schema 隔离；
- 服务间 HTTP 通过环境变量和 Compose DNS 静态发现；
- 内部资产接口只使用共享静态 Token；
- 没有服务级限流、熔断、重试预算和 mTLS；
- Gateway 是轻量 Reverse Proxy，不是成熟 API Gateway 产品；
- Governance Service 尚无领域逻辑。

### 5. RabbitMQ 可能被“问穿”的点

最危险的问题是：“你说可靠消费，那 ACK 在哪里？”

当前 ACK 位于任务结果/可靠重试事务提交之后，并且绑定接收消息的原 channel。数据库不可用或提交不明时关闭 channel 退避重连；旧租约恢复与副作用幂等仍必需，不能只靠 ACK。

第二个问题是：“DLX 是怎么触发的？”

业务死信与 dead_letters 在同事务保存 dead Outbox，独立 publisher 发布。Broker DLX 同时保留 task.requested/task.dead 绑定，但不能替代数据库可靠死信记录。

### 6. 大批量导入导出的性能瓶颈

- API 仍以 JSON 承载 CSV，HTTP 层会承受完整原文，但任务 JSONB 已只保存 MinIO 引用；
- 预检和执行各解析一次；
- Worker执行阶段保留全部规范化行；
- 错误明细已迁到 MinIO JSONL，但缺少错误报告分页预览；
- 批量 Insert 动态生成大量参数，宽表下受 PostgreSQL 参数上限约束；
- 导出 Offset 深分页；
- 导出先聚合全部 `[]Record`，再聚合完整 CSV Buffer；
- MinIO Put 接口只接受 `[]byte`，没有流式或 multipart；
- worker 默认两个 slot，可配有限任务并发；同任务批次顺序提交，资源配额与自动扩容尚未落地。

### 6.1 一个需要优先修复的权限问题

导入任务的 `department_id` 可以由请求传入，任务服务已在 `Create` 路径根据 IAM AuthorizationContext 校验部门，非全局用户不能提交范围外部门。剩余风险是资产服务内部批量接口仍只校验共享服务 Token 并信任任务服务传入的 owner/department；生产环境还应使用可轮换、可审计、最小权限的服务身份，并考虑传递可验证授权上下文。

### 7. MinIO 有没有解决文件生命周期

已为 `imports/`、`import-errors/`、`exports/` 配置统一可调的保留天数，并在任务创建失败和幂等竞争时主动删除孤儿对象；源 Key 也包含内容 SHA-256。仍缺对象级过期元数据、删除审计、存储配额、预签名下载和最小权限账号，不能说已经有完整文件治理。

### 8. 是否缺少分布式链路追踪

缺少。HTTP 有 request_id，但 MQ Event 没有传播标准 Trace Context；也没有 OpenTelemetry Exporter。只能依靠 taskId、eventId、requestId 和容器日志人工串联。

### 9. 是否缺少配置中心和服务发现

缺少。Compose 环境下用服务名解析，URL 和密码通过环境变量注入。这对少量内部服务够用，但规模扩大或多环境部署后要引入 Kubernetes ConfigMap/Secret、服务发现或集中配置治理。

### 10. 当前到底算不算微服务

比较准确的说法是：**从冻结单体向领域服务演进的新平台，已经具备基础微服务形态，但生产治理成熟度有限。**

它不是纯模块化单体，因为确实有独立进程、HTTP/MQ通信、独立 Schema 所有权和独立镜像；也不是成熟微服务平台，因为治理服务未落地、共享基础设施较多、缺少生产级服务治理和可观测性。

---

## 14. 面试速记版

## 项目一句话

公司内部资产管理平台的渐进式服务化改造：用 Go、PostgreSQL、RabbitMQ 和 MinIO 支撑七类资产、权限治理及异步 CSV 导入导出，为后续网络安全通报整改闭环提供基础。

## 项目背景

旧系统集中管理资产和漏洞台账；下属单位多，批量报送容易导致同步接口超时；未来还要承接漏洞通知和整改状态。旧系统冻结，新业务迁移到新平台。

## 我的工作

Go 服务和统一中间件；Schema 驱动资产 CRUD 与 PostgreSQL；任务状态机、Outbox/Inbox、批量幂等、RabbitMQ 与 MinIO；Docker Compose、OpenAPI、测试和容量验收。

## 为什么微服务

IAM、在线资产、批量任务和整改流程的生命周期与资源模型不同，需要隔离变化和故障。项目规模不大，所以只拆少量边界清楚的服务，没有引入重型治理平台。

## 为什么 RabbitMQ

把耗时导入导出从 HTTP 解耦，任务排队削峰，Worker 按能力消费。相比 Kafka 更贴合任务队列；相比 Go Channel 能跨进程、持久化。

## 为什么 MinIO

导入源、错误报告和导出文件不适合放 PostgreSQL BLOB/JSONB 或容器本地盘。MinIO可私有化、S3 兼容、容器环境共享，并通过前缀生命周期统一清理。

## 导入流程

提交 JSON 内嵌 CSV → IAM授权与部门范围校验 → 源文件写 MinIO → 创建任务引用 → 从 MinIO 同步流式预检 → 错误 JSONL/待确认 → 确认与 Outbox 同事务 → RabbitMQ 小消息 → Worker从 MinIO 重解析 → COPY 行记录 → 每 500 行资产事务 → 幂等/版本 → 成功或重试死信。

## 导出流程

创建任务与 Outbox → RabbitMQ → Worker按权限每 5000 行查询 → 内存生成 CSV → 上传 MinIO → 任务记录 Object Key → 用户轮询 → 授权下载。

## RabbitMQ 可靠性

有 durable exchange/queue、persistent message、事务 Outbox/Inbox、重试和死信；AMQP manual ACK、prefetch=1、Confirm/Return、执行租约与恢复已实现。消息仍是至少一次投递。

## 幂等

任务唯一键 + Outbox Event ID/调度代次 + Inbox + token/租约 + 导入 rowKey + asset.import_idempotency 原子占位 + 批次事务。新 taskId 下的业务重复仍需业务唯一键解决。

## PostgreSQL 优化

强类型表和索引；500 行多值 Insert；任务行用 COPY；唯一约束兜底；深分页需改 Keyset/游标，超大导入可用 staging + COPY + merge。

## Go 并发

不为每行创建 Goroutine。当前 worker 默认两个 slot 并发任务；同任务批次顺序处理，扩容需结合 Context、租约与数据库连接池背压。

## Docker 部署

Go 和 Vue 都是多阶段构建，运行阶段非 root；Compose 编排 PostgreSQL、RabbitMQ、MinIO、七个 Go 运行进程、一次性迁移和 Web，通过服务名通信并用命名卷持久化。

## 当前项目不足

整改通知未实现；CSV 而非 Excel；API 仍用 JSON 承载完整 CSV；新版故障与容量验收需与旧版记录区分；导入执行与导出仍有全量内存问题；无预签名 URL、Trace、配置中心、服务发现和生产高可用。

---

## 15. 最值得重点讲的 5 个技术点

1. **Schema 单一事实源**：七类资产从 YAML 生成/约束 Go、OpenAPI、TypeScript、Vue 和 PostgreSQL，解决字段漂移和导入绕过校验。
2. **事务 Outbox + 多层幂等**：任务创建与事件同事务，任务、事件、消费、导入行和资产写入各有数据库约束。
3. **10 万行批量导入的工程取舍**：流式预检、用户确认、500 行事务、COPY 行记录、失败重试及部分提交恢复。
4. **权限贯穿导出链路**：IAM实时授权，任务固化数据范围，资产服务内部分页仍应用 owner/department/global 和敏感字段权限，下载再校验任务所有者。
5. **诚实识别可靠性缺口**：已把提前 ACK 的 HTTP 消费替换为 AMQP manual ACK、Confirm/Return 和租约恢复，并能解释至少一次投递与副作用幂等边界。

## 16. 最需要提前准备的 10 个追问

1. 为什么新平台不用 Gin，而用 `net/http`？
2. 你说 10 万行流式导入，为什么 Worker 还把全部行放内存？
3. RabbitMQ 的 manual ACK 和 publisher confirm 在哪里？
4. Consumer 拉到消息后立刻宕机，当前系统如何恢复？
5. 一批资产写成功、任务状态没更新，如何避免重复？
6. 为什么导出使用 Offset，几十万行怎么优化成 Keyset/游标？
7. MinIO文件如何过期、清理和授权？当前为什么没有 Presigned URL？
8. 漏洞整改通知和状态机到底实现了吗？如果没有，你会怎样设计？
9. 所有服务共享一个 PostgreSQL 实例，还算微服务吗？
10. 没有 Trace、配置中心和服务发现，生产环境如何排错和扩展？

---

## 17. 面试前最后检查

- 把“Excel”统一改成“当前 CSV、目标 Excel”；
- 可以说“CSV 源文件和错误报告存 MinIO、任务 JSONB 只存对象引用”，但不能说已经支持 Excel/客户端直传；
- 不要说治理服务已经有整改通知 CRUD；
- 可以讲已实现 manual ACK、Confirm/Return、租约和恢复；不要把它描述成 exactly-once 或未经验证的生产高可用；
- 导入进度按累计提交更新，created/skipped 与 committed 的口径要分清；
- 不要说导出完全流式，当前全量 Record 和 CSV 都在内存；
- 可以说 MinIO 有统一前缀生命周期；不要说已有预签名下载、对象级审计或配额；
- 不要说系统有 Kubernetes、配置中心、服务发现和分布式 Trace；
- 能讲清楚一个真实失败窗口和对应改造方案，比堆砌“高可用、削峰、解耦”更有说服力；
- 所有“我主导”都要能对应个人真实提交、设计文档或上线责任。

## 18. 代码事实索引

| 事实 | 代码位置 |
|---|---|
| 七个 Go 运行进程、迁移入口和端口 | `platform/cmd/*/main.go`、`compose.yaml` |
| `net/http` 中间件和优雅关闭 | `platform/internal/servicekit/http.go`、`server.go` |
| IAM 和 Refresh 轮换 | `platform/internal/iam/` |
| 七类资产 Schema | `schemas/assets/`、`platform/internal/assetschema/schema_gen.go` |
| 资产 CRUD、权限和 SQL | `platform/internal/asset/` |
| 任务状态与 Outbox/Inbox | `platform/internal/taskcenter/model.go`、`migrations/001_init.sql` |
| AMQP 与独立 publisher | `platform/internal/taskcenter/broker_amqp.go`、`publisher.go`、`store_outbox.go` |
| 导入/导出 Worker | `platform/internal/taskcenter/worker.go` |
| 500 行批量事务与幂等 | `platform/internal/asset/batch_fast.go` |
| MinIO S3 签名 Put/Get | `platform/internal/taskcenter/object_store.go` |
| Vue3/Pinia/Element Plus | `apps/web/` |
| Docker 和 Compose | `platform/Dockerfile`、`apps/web/Dockerfile`、`compose.yaml` |
| 10 万行与端到端验收 | `scripts/phase3-capacity.ps1`、`scripts/phase3-compose-smoke.ps1` |
| 漏洞台账字段 | `schemas/assets/vulnerability.yaml` |
| 治理服务仍是骨架 | `platform/cmd/governance-service/main.go` |
