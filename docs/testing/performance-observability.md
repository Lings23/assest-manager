# Go 服务性能观测（P2）

本页定义实际观测边界；实验结果及未验证项见 `performance-optimization-p0-p3-results.md`。不改变连接池预算、事务、授权或 AMQP 可靠性。

## 访问与标签

各 Go 服务提供 `GET /metrics/service`，默认只接受真实 RemoteAddr 为回环地址的请求，不信任 X-Forwarded-For。可由显式进程环境 `METRICS_TOKEN` 启用 `X-Metrics-Token` 验证。Gateway 上公开访问默认403。原 Publisher/Worker 的 `/metrics` 保留。

本机采集通过 `docker exec <优化项目容器> wget -qO- http://127.0.0.1:<服务端口>/metrics/service`，没有新增宿主端口或写出访问 Token。端口为 Gateway8080、IAM8081、Asset8082、Governance8083、Task Report8084、Publisher8085、Worker8086。

HTTP 标签使用 ServeMux 注册模式、有限方法和状态码；未命中统一为 unmatched，未知方法为 OTHER。Asset 使用 `{type}`/`{id}` 模板；Gateway 使用注册的转发前缀，因此粒度比 Asset 路由粗。SQL操作、池名、上游组件和Worker阶段均由有限白名单控制。资产ID、task_id、Token、Request ID、SQL及指纹都不作为指标标签。

## HTTP、上游及数据库

| 指标 | 含义及边界 |
| --- | --- |
| `service_http_requests_total` | 按服务/模式/方法/状态累计；`rate(...[1m])`得到服务器RPS，包含健康检查，业务RPS需筛选路由 |
| `service_http_duration_seconds` | handler生命周期直方图；含嵌套授权和SQL，不含返回handler后的最终网络刷新 |
| `service_http_inflight` | 相同模式/方法的正在处理数量 |
| `service_http_timeouts_total` | handler可见的上下文超时或写入超时；不包括进入handler之前的连接/请求头超时 |
| `service_upstream_duration_seconds` | 从RoundTrip开始至响应体EOF或Close；包含消费者读取节奏，不能当纯网络/远端CPU耗时 |
| `service_upstream_timeouts_total` | 客户端调用和响应体读取的超时 |
| `service_upstream_response_body_bytes_total` | 实际读取的响应体字节，不含HTTP头 |
| `service_upstream_declared_request_body_bytes_total` | 正ContentLength之和；不是重传/分块在内的实际线缆流量 |
| `service_database_duration_seconds` | Conn查询/COPY至结束，包含行消费；不含连接池获取。ACQUIRE操作单列连接获取/新连接构造时间 |
| `service_db_pool_*` | Max/Open/InUse/Idle/Constructing及成功获取、取消获取、空池等待次数与时间 |

`service_db_pool_wait_seconds_total` 使用 pgxpool `EmptyAcquireWaitTime`，不把整个 `AcquireDuration` 当作纯等待。它只累计成功空池等待，正在等待和取消的请求须结合 InUse、ACQUIRE直方图及 canceled_acquires_total 判断。Asset与IAM的池上限继续为10，Task池保持原默认，不通过观测改动扩大。

分位数用 `histogram_quantile(0.95, sum by (le,service,route)(rate(service_http_duration_seconds_bucket[5m])))` 等计算；桶内插值是估计，验收延迟使用k6原始分位数和最低样本要求。不能平均各实例p95，不能把Gateway、IAM上游、Asset handler和SQL耗时简单相加。

单次数据库调用至少100ms时写 `slow database operation` 日志：operation、耗时、请求ID、失败标志、SQL文本的MD5指纹，不写SQL参数/原文。MD5仅作与 `pg_stat_activity` 对照的诊断键，不作安全用途或指标标签。HTTP Request ID跨Gateway及内部HTTP传播，便于日志关联。

## 运行时及异步任务

输出 Goroutine、HeapAlloc、HeapInuse、GC累计次数/暂停总时间。Linux通过 getrusage采集进程CPU秒、`/proc/self/statm`采集RSS；不支持的平台明确输出 availability=0，不拿Go Sys伪装RSS。Docker MemUsage、进程RSS、Go Heap是不同指标。

Worker补充有限阶段直方图：import_parse、import_record_rows、import_asset_batch、import_mark_progress、import_final_audit、export_read、export_encode、export_put。parse包含从对象响应体读取及解析；object_store上游span也可能包含这段读取，二者重叠。每批事务、累计进度、行级幂等、租约/执行Token及ACK不变。

dispatch_wait使用成功认领时数据库租约时刻减原租约时长，得到认领时刻，再减 `next_run_at`；不计计划中的重试延时。这段包含发布、队列等待与认领调度，不能称作纯RabbitMQ排队时间，也不能与最老Outbox年龄相加。它与执行时长分别记录。复用原taskcenter指标的Outbox未发布/最老年龄、active executions、claim conflicts、lease expirations、execution duration、dead letters及AMQP unacked/confirm等。

增强采集器每约15秒读取服务指标、PG活动与锁阻塞、WAL/IO，以及数据库累计额外执行尝试 `sum(greatest(attempt-1,0))`。此项包含故障恢复后的再次执行，不应表述为仅业务错误重试；数据库恢复快照会重置，应按实验窗口处理。PG仅把active/idle-in-transaction的等待用于分析，不把正常空闲 ClientRead 计为锁竞争。IO timing配置未启用时，相应时间为不可用证据，不能据零值认定没有IO等待。

## pprof与可复现检查

`DEBUG_PPROF_ADDR` 默认空，只有显式配置才启动独立服务；配置校验要求字面回环IP，不接受0.0.0.0或主机名。它不注册在公共Mux，Gateway的`/debug/pprof/`继续404。Docker调试需显式单服务环境覆盖，保持端口不向公网发布；容量测试不能暗中打开profiling后把结果归于应用优化。

`metrics_test.go`验证模式归一化、Token/回环访问、响应体完成、原Transport与超时；`metrics_database_integration_test.go`在真实PG上强制一个测试连接，验证成功等待、取消和查询/获取分段。生产预算不改为测试用的1。

`scripts/perf/metrics-verify.py`通过真实容器和已执行的CRUD/导入/导出验证指标、Worker阶段、10连接预算及外部拒绝访问，保存原始Prometheus文本。`collect.ps1 -Enhanced`输出每次配置/脚本哈希及采样时间；其OutDir必须与压测OutDir下的telemetry目录一致，才能识别恢复快照的maintenance.flag。

## P2 压测复现与诊断边界

本次入口是 `scripts/perf/optimization-p2.ps1`，只允许隔离项目 `asset-amqp-opt` 及其 `asset_perf` 数据库。已有归档不能覆盖；恢复使用 `-Resume`，新的实验需复制冻结快照、工具版本记录并更换整套归档路径。真实实验的镜像、源码、配置、脚本哈希和状态保存在 `data/perf/optimization-p2/`。功能测试与写入压测必须串行。

容量控制器 `optimization-capacity.ps1` 使用原 `api.js` 和 `run.ps1`，每点恢复 D1、固定 search 索引、原账号池和业务比例。短测预热60秒/稳态180秒，遇到首个未通过点停止升压，再对已通过候选点独立重复三次，每次预热60秒/稳态600秒。原认证使用每场景128个预分配/最大VU；若600秒候选未通过普通性能门槛，保留失败并验证更低的已通过短测点；健康、资源或正确性停止不允许自动降档继续。

原脚本的每个VU在首次请求时创建独立登录会话；预热和稳态是不同场景，会分别产生首次登录。提高VU容量会同时增加会话与私有更新夹具，不能当成只改变调度能力的纯实验。完整点的 dropped_iterations 必须保留，即使后续分钟达到目标RPS也不能追认通过。当前未修改成预认证模型，未降低密码哈希成本，也未共享刷新会话来消除失败。

`auth-burst-diagnostic.py` 是独立登录诊断，不能用于业务容量认证。临时pprof仅绑定IAM容器回环，完成后必须移除覆盖、恢复原配置并验证监听关闭；它与正式认证分开归档。密码、Cookie和访问/刷新Token不写入结果。

离线汇总顺序为 `capacity-summary.py --results-dir data/perf/optimization-p2`、`resources-summary.py --results-dir data/perf/optimization-p2 --output-json data/perf/optimization-p2/resources.json`，最后用独立绘图库环境运行 `plot-capacity.py --results-dir data/perf/optimization-p2`。这些程序只读取原始记录，不产生新HTTP或SQL流量。`optimization-p2-final.ps1` 则必须等容量控制器完成并恢复D1后才能运行，执行真实PG/AMQP回归、权限检查、最终D1恢复与静态检查点。

2026-10-18下午用户另行授权将所有VU首次认证移入setup，并明确从240开始、不复测160。该追加协议由`api-preauth.js`和`optimization-preauth.ps1`实现，原api.js及旧认证记录不变；独立会话、Cookie交付、过期/刷新保障、零窗口认证约束与单列归档详见[预认证业务实验](performance-preauthenticated-business.md)。不能把新旧施压协议的容量差算成应用优化收益。
