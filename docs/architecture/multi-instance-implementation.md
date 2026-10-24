# 多实例实施与验收

日期：2026-10-24。改造前完整工作区已提交为 `689cd65`。本轮落实调研方案的应用改造、预检恢复和单机 HTTP 多副本阶段；没有切换现有 `asset-amqp-opt` 项目及其数据。原调研与选型文档保留为设计时记录。

## 实现结果

| 范围 | 当前行为 |
| --- | --- |
| 公共入口 | 固定 digest 的 HAProxy 3.2.10，直接调度 IAM、Asset、Task API、Governance 和 Web；仅入口发布业务端口。Gateway 退出默认部署，源码与 `gateway-rollback` profile 保留作为回切工具。 |
| 内部调用 | Asset/Task API 的 IAM 调用走 `service-lb:18081`，Task API/Worker 的 Asset 调用走 `:18082`。内部监听和监控不映射宿主端口。 |
| 发现和健康 | Docker DNS + 8 个模板槽位/服务，round-robin，无粘性会话；2 秒检查、失败 2 次摘除、成功 2 次恢复。所有代理重试关闭。 |
| IAM 密钥 | Compose Secret 将同一持久 RSA 私钥只注入 IAM。缺少密钥启动失败；仅显式 `IAM_ALLOW_EPHEMERAL_KEY=true` 允许独立开发进程生成临时密钥。issuer/audience 统一。 |
| 登录限制 | PostgreSQL 原子预留将失败及在途尝试一起纳入“账号＋可信 IP”的 1 分钟固定窗口，限额 5。成功只释放自己的预留，不清掉其他失败；重启不清零，存储不可用返回 503。过期窗口惰性重置，每分钟分批清理。 |
| 客户端地址 | 公共入口覆盖外部 X-Forwarded-For/Proto，删除 Forwarded/Forwarded-Host。IAM 仅信任配置 CIDR 内的代理，从右向左解析链；不信任直连客户端伪造的头。 |
| 刷新 | 同页共用一个 Promise，迟到的 401 可使用已更新 Token；Web Locks 串行化跨标签页的 Cookie 轮换。刷新失败不自动重放，要求重新登录；不支持 Web Locks 的浏览器直接重新认证。真实旧 Token 重放仍撤销 family。Token 仍只保存在页面内存。 |
| 初始化 | IAM/Asset/Task 显式 migrate-only，事务 advisory lock 在第一次建 Schema 前生效，版本及规范化校验和登记。IAM 管理员依赖唯一约束和冲突后查询实现并发幂等。Asset 纳入原有 004/005 索引；运行时不执行 DDL。 |
| 对象存储 | Bucket/lifecycle 由 task-migrate 的 `--init-objects` 初始化；API/Worker 启动不再修改生命周期配置。 |
| 就绪和排空 | liveness 不探测依赖；IAM/Asset/Task ready 只检查自身 DB，800ms 超时、合并并缓存 1 秒。Task 不因 S3/Asset 中断而整体摘除，具体业务仍检查授权和依赖。收到终止信号后先 ready=503，等待 7 秒，再关闭接入并等待在途请求。 |
| 导入恢复 | API 只保存源文件、任务和预检 Outbox。Worker 的预检和导入使用不同 dispatch version，共用现有认领、续租、Inbox、恢复和 token 防护。预检完成进入 awaiting_confirmation，确认后清零 attempt 并增加代次；不会自动导入。 |
| 预检结果 | 报告键包含执行 token；仅有效租约可发布结果指针。预检运行中可取消；迟到完成不能覆盖取消或新执行。旧 generation-zero 中断预检在 003 迁移中单次转为可恢复任务。 |
| 幂等 | API 摘要覆盖 kind、资产类型、有效部门和源文件 SHA-256。相同 key/内容复用任务，不同内容 409；老任务缺少摘要时同键拒绝复用，需换 key。正式导入沿用资产 `(task_id,row_key)` 事务幂等。普通资产创建未增加自动重放能力。 |
| 前端 | 新增 CSV 提交、预检轮询、错误报告下载、手动确认和取消。DTO/OpenAPI 增加 phase=preflight/execution；旧验收及任务性能脚本改为等待异步预检。 |
| 静态资源 | web-assets 初始化任务把新哈希资源追加到共享 `web_assets` 卷，Web 只读服务该卷；旧哈希不覆盖、不随新版本删除，HTML 不缓存。允许新旧 Web 引用各自资源；生产可改用 CDN/对象存储。 |
| 观测 | 响应提供 X-Instance-ID，日志含实例和版本，入口请求 ID 贯通服务调用。验收直接逐实例抓取 `/metrics/service`，不经 LB 抓取。 |

HAProxy 的 public 路由按精确前缀边界白名单分域；未知 API、内部/metrics/debug 路径返回 JSON 404。CORS、单份凭据头、请求 ID、上游错误封装和请求体限制在入口实现。请求 ID 仅保留 1–128 位字母、数字、点、下划线、冒号、连字符，其余重新生成。HTTP 协议解析前的错误不保证已有请求 ID。静态页面继续保留 Nginx 安全头。

## 资源和时间预算

| 进程 | 常态副本 | 每副本 DB 上限 |
| --- | ---: | ---: |
| IAM | 2 | 5 |
| Asset | 2 | 5 |
| Task API | 2 | 4 |
| Publisher | 2 | 2 |
| Worker | 2（每个 1 slot） | 3 |

常态池上限合计 38；五类进程各增加一副本时为 57。迁移器各限制 1 个连接，管理员初始化使用独立短生命周期池；运维、监控和保留连接仍需另外预留。`DB_MAX_CONNS` 可设置 1–100，优先于连接串；未设置时保留连接串的 pool_max_conns，再采用域默认。不要同时增加副本、连接数和 Worker slots 后宣称收益来自副本数。

普通请求 context/I/O deadline 30 秒，导入上传及下载/错误报告为 120 秒；服务 read/write 上限 120 秒，HAProxy client/server 为 125 秒。编码后 body 的总上限为 100 MiB，IAM/Asset 的普通写入另限 1 MiB；Content-Length 与 chunked 超限均使用 413 错误封装。

HTTP 排空等待 7 秒、shutdown 预算 130 秒、容器宽限 150 秒。Worker 仍保持 60 秒 drain、75 秒容器宽限。调整文件期限时同步调整 read/write、HAProxy 和 HTTP shutdown/grace；连接占满或数据库故障不意味着需要重启进程。

## 启动与隔离验收

新开发环境：

```powershell
Copy-Item .env.example .env
# 编辑 .env，替换所有占位密码
.\scripts\init-deployment.ps1
docker compose up --build -d --wait
```

密钥生成只创建新文件，已存在时不覆盖；密钥在被忽略的 data/secrets 下，应按目标主机管理其文件权限和备份。首次切换临时密钥会要求重新登录。生产入口须添加真实证书/TLS listener、允许的 CORS 域名并设置 Secure Cookie；本轮实验使用 localhost HTTP，尚未验收生产 HTTPS、证书或跨主机接管。

独立实验：

```powershell
.\scripts\multi-instance-smoke.ps1
# 已有独立实验可重跑，保留运行状态便于检查：
.\scripts\multi-instance-smoke.ps1 -ComposeProject asset-multi-20261024 `
  -EnvFile data/multi-instance/.env -SkipBuild -KeepRunning
```

脚本需要 Docker Compose >=2.24.4，使用 `!reset`/`!override` 清掉基础设施映射，并检查合并结果只有 service-lb 发布端口。随机凭证、持久密钥和日志保存在 data/multi-instance；默认停止项目并保留卷，不执行 down -v。默认项目、网络和端口必须与已有项目区分。

本轮项目为 `asset-multi-20261024`，入口 `127.0.0.1:59180`，实验网络 `172.30.241.0/24`，各数据卷独立。集成测试另用同一实验基础设施里的 multi_tests 数据库与 multi-tests RabbitMQ vhost，避免应用 Worker 消费测试消息。测试日志在 `data/multi-instance/`；逐项 HTTP 证据及实例指标在 `data/multi-instance/asset-multi-20261024/`。

## 兼容升级与回退

1. 保存源码/镜像摘要、数据库备份和密钥；本轮已经保存改造前源码基线。没有对现有 D1 大数据卷做复制或性能实验。
2. 检查既有 Docker 网络实际 CIDR 并正确设置 APP_SUBNET；若需更换网络，在独立窗口重建网络并保留数据卷。切换到新 Compose 前不要假定已有网络会自动改 CIDR。从旧同步预检版本首次升级，先停止旧 Task API、Publisher、Worker，再执行新 task-migrate。003 会恢复旧中断预检，不能与旧执行者混跑。IAM/Asset 首次登记迁移和创建索引同样安排独立窗口，避免重负载下阻塞写入。
3. 执行 IAM、Asset、Task 三个迁移任务，确认退出成功，再启动新服务。之后仅在新旧代码均兼容当前 Schema 和异步任务协议时滚动。
4. 按域滚动：

```powershell
.\scripts\rolling-deploy.ps1 -ComposeProject asset-multi-20261024 `
  -EnvFile data/multi-instance/.env -Service asset-service `
  -ComposeFiles compose.yaml,deploy/test/compose.multi-instance.yaml -SkipBuild
```

脚本先增加一个有服务 DNS alias 的临时实例，确认 ready 并等待健康传播，再逐个 SIGTERM 排空旧容器、补齐并验证替代实例，最后撤下临时实例。至少需要 2 个常态副本，8 个 HAProxy 槽位中须保留 1 个给临时实例。它不执行迁移；在运行前先完成兼容迁移和发布峰值预算核对。脚本失败时停止继续替换并保留已就绪的临时实例和正常实例，需按实际进度处理，不能恢复旧数据库覆盖新写入。

回退入口时可启用 gateway-rollback profile，经其容器端口访问原公共协议；该 profile 不发布宿主端口，流量回切需显式配置。任务代码只能回退到支持异步预检和当前代次协议的版本；不能直接回到 689cd65 后让旧 Worker 消费新预检消息。IAM 密钥、Schema 和新数据继续保留。Web 资源卷需要随环境备份；清理历史哈希文件应独立安排缓存有效期和活跃页面的保留策略。

## 验收范围

可随源码审查的脱敏结果见 [2026-10-24 验收摘要](../testing/multi-instance-acceptance-2026-10-24.json)。本轮 13 项 HTTP 验收全部通过；SIGKILL 场景单次观察到约 3.094 秒后连续稳定响应，不作为持续 SLO。实际逐实例滚动替换期间，594 个未认证探测均返回预期 401，没有网络或代理上游错误。当前 JS/CSS 经入口加载成功，重新初始化后历史静态资源仍可读取。


已通过的平台检查：Go 全量单测/vet；真实 PostgreSQL/RabbitMQ 集成；前端 typecheck、6 项单测及 build；Contract/Schema 生成一致性；OpenAPI 有效（保留历史 3 条非阻塞警告）。原始根目录 `go test ./...` 会扫描本机忽略的 data/perf 草稿；按实际源码目录 `. ./internal/... ./tools/...` 执行的根模块 test/vet 通过，未修改这些草稿。Docker 运行镜像用固定 Go 1.25.13 构建，本机补充测试工具链为 Go 1.26.5。

真实数据层测试覆盖：8 个首次迁移并发只应用一次、校验和漂移拒绝；运行时以只读事务配置打开 IAM，证明启动不再执行写入/DDL；30 个并发登录预留跨池最多放行 5 个，重连不重置；预检租约恢复、旧 token 完成拒绝、取消后报告拒绝、确认重置 attempt 并增加代次，以及原有 AMQP 副作用和恢复回归。

HTTP 脚本覆盖：跨副本 Token、跨副本刷新及真实重放、伪造转发头、重启后全局限流、并发资产更新、同键异内容、API 强杀后预检恢复、确认前禁止正式导入、授权错误报告、SIGKILL 摘除、SIGTERM 排空、扩缩容 DNS 变化、S3/Asset 故障时路由隔离、直接逐实例指标。测试结果以 acceptance.json 及日志为准；首次 keep-alive 30 次请求在 IAM/Asset/Task 各观察到 15/15 分流，属于该场景观察，不是任意负载均匀性保证。

本轮没有进行持续容量认证、独立施压机复跑、跨主机/Kubernetes/入口冗余、PostgreSQL/MQ/S3 高可用、网络 DNS 故障注入或生产 RPO/RTO 演练。旧稳定容量 320 RPS 的证据边界不变。阶段 E 需要实际主机资源、业务比例、目标 SLO 和故障域后再实施；单机多实例仍存在宿主、入口和数据层单点。
