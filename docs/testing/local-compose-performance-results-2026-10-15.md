# Docker 版本核验与本机压测执行记录

执行日期：2026-10-15（Asia/Shanghai）。依据：[本机 Docker Compose 压力测试方案](local-compose-performance-plan.md)。

**结论：当前工作区已完成镜像重建，功能和单次容量链路通过；完整性能基线未完成。** 正式读取测试被宿主内存停止线中断，不能据此给出稳定容量、p95/p99 或压测达标结论。用户确认另有 Python 实验运行，当前无法释放足够内存；本轮已结束施压并停止独立测试环境。

## 1. Docker 中的版本状态

开始检查时没有运行中的容器。原 `asset-governance` 的服务容器均已停止，创建时间为 2026-08-28；其配置尚未包含当前独立 publisher/worker 部署，不能视为当前工作区版本。

本次使用原始 Compose 和 Dockerfile，从当前工作区构建并启动了独立项目 `asset-amqp-perf`。后端构建工具链核验为 **Go 1.25.13**；所有服务及 Web、Gateway 健康检查通过。

| 服务 | 原项目镜像 ID（缩写） | 本次测试镜像 ID（缩写） |
| --- | --- | --- |
| asset-service | f0d33d67c4bc | 3fb16ad68553 |
| gateway | 6f69b2318f4c | eb50d78751e6 |
| task-report-service | 8269eb7868cc | 26e27c15dd29 |
| task-publisher | 原项目无对应容器 | c720cca906ba |
| task-worker | 原项目无对应容器 | 28151f59abbc |

旧、新资产服务二进制的 SHA-256 前16位分别为 `97AA32E742D17512`、`639E9A1F6B714C28`。构建日志、镜像清单、二进制完整摘要和 `go version -m` 输出均已归档。Docker 构建可以复用缓存，所以镜像日期本身不作为唯一版本证据。

这里的“当前版本”指当前工作区源码，包括未提交改动；HEAD 为 `b33392e0cc46d1487e7a5127dcb458706c848a40`。本次没有升级原项目的数据卷或启动原项目旧容器；测试镜像与数据均属于独立项目。

## 2. 实测环境

| 项目 | 记录 |
| --- | --- |
| 宿主 CPU | Intel Core i5-12400F，6核/12逻辑处理器 |
| 宿主内存 | 约31.9 GiB |
| Docker | Engine 29.6.2；Compose 5.3.1 |
| Docker 可用额度 | 12逻辑处理器、约15.55 GiB内存 |
| PostgreSQL | 16.14，max_connections=100 |
| worker | 1副本×2slots；手动ACK，prefetch=1 |
| k6 | Windows v2.3.0；官方发布校验和验证通过 |
| 入口 | Gateway 59080，Web 59088；其余端口按方案隔离 |
| 同机干扰 | 另有 Python 实验，内存和CPU占用明显，不符合稳定基线所需的空闲条件 |

只读进程元数据表明相关 Python 实验来自 `D:\workspace\FL2`。本次没有终止这些实验。两次采集到宿主可用内存连续30秒低于15%后，停止标记均已保存；第二次停止时可用内存约2.35 GiB（7.37%），后续复查约2.00 GiB（6.26%）。曾短暂连续30秒恢复到15%以上，但未能在正式测试期间持续维持。

## 3. 已完成的真实验证

| 验证 | 实际结果 | 可得结论 |
| --- | --- | --- |
| 当前源码构建与服务启动 | 全部服务构建成功，健康检查通过 | 当前源码可在该Compose环境运行 |
| 现有阶段三功能冒烟 | 导入1行、提交1行、导出1行；2个消费者；Inbox=3；预期死信=1；统计7类 | 基本权限、文件、消息及任务链路通过 |
| 现有10万行容量链路 | **19.60秒**；succeeded；attempt=1；created=committed=100000；skipped=0 | 本机一次完整导入成功；不代表分位数或最大容量 |
| 10万行只读SQL对账 | 幂等键、解析行、已提交行、有效资产及初始导入版本各100000；同键任务1；未发布事件0；死信0 | 本次任务未发生丢行或重复副作用 |
| 新任务测试脚本真实冒烟 | 一次导入、一次导出均通过；CSV实际解析、幂等重放与SQL核验通过 | 新脚本已在真实依赖上做基本验证 |
| D0固定数据准备 | 七类10000行；9500有效、500软删除；1000条达到5版本；20个测试账号；140个导入任务 | 数据准备和各账号真实统计数量对账通过 |
| k6短时工具校验 | 预热10秒、稳态30秒；目标5RPS，实际5.033RPS；稳态151请求全部通过；掉迭代0 | 会话、读取断言与采集可用；每端点仅约50样本，不能作为正式基线 |

10万行任务 ID：`a934d170-0089-4660-9bcb-3755d96bb26b`。计时沿用现有脚本：包括上传、预检、确认、排队、执行和1秒间隔完成轮询，不包括CSV生成、登录、重放及事后SQL对账。测试当时宿主存在其他实验，因此不与历史19.57秒做性能回归判断。

新任务脚本的冒烟发生在容量任务之后，其导出读取了当时约10万行的完整可见集合；该冒烟验证文件和任务正确性，没有作为导出吞吐基线。

## 4. 中断与未完成项

正式D0读取测试计划为：5RPS、预热60秒、稳态180秒，运行标识 `s1-d0-read-5-r1`。2026-10-15 **22:24:33**，采集器再次写入资源停止标记，编排器随即停止k6。该轮没有完整稳态汇总，标记为环境中断，既不计应用性能失败，也不计达标。

尚未完成：D1十万行多类型数据集、完整S1阶梯与三轮重复、S2深分页/搜索、S3读写混合、S4统计负载、S5/S6多轮并发导入导出、S7混跑对照、S8扩worker、S9突发和60分钟持续运行、S10并发正确性及可选故障注入。单次10万行成功不能替代这些场景。

## 5. 新增执行工具及验证范围

- [seed.ps1](../../scripts/perf/seed.ps1)：固定种子、七类资产、账号/部门、版本和删除样本。D0真实执行通过；D1尚未执行。
- [api.js](../../scripts/perf/api.js)：k6读取、搜索、混合、统计、突发和持续场景。真实短时读取通过；其他模式尚无本轮正式结果。
- [jobs.ps1](../../scripts/perf/jobs.ps1)、[verify.sql](../../scripts/perf/verify.sql)：分段计时、CSV解析、幂等和SQL对账。真实冒烟及10万行只读SQL核验通过；完整并发场景尚未执行。
- [collect.ps1](../../scripts/perf/collect.ps1)：资源、任务指标和数据库采样；资源停止线已实际触发并验证。
- [run.ps1](../../scripts/perf/run.ps1)：资源门槛、k6编排、升压停止与中断记录。真实调用通过，正式测试中验证了停止行为。
- [database.ps1](../../scripts/perf/database.ps1)：仅允许隔离项目，保存与恢复数据库快照。初始库恢复、D0快照保存通过；新增的“从宿主持久快照复制回容器”续跑路径仅做语法检查，待恢复环境时验证。

全部PowerShell文件通过语法检查，k6脚本通过Node语法和k6配置检查。模拟服务结果仅用于脚本开发，不计入上述真实测试结果。

## 6. 归档与续跑

完整证据目录为 [data/perf/20261015-local-baseline](../../data/perf/20261015-local-baseline/)，已被Git忽略：

- `execution-status.json`：本轮状态为 `incomplete_resource_limited`。
- `build.log`、镜像/二进制清单、源码摘要与工作区diff：版本证据。
- `legacy-capacity-100k.log`、`capacity-observed-audit.json`：容量任务输出及当时观察到的SQL结果。
- `seed-d0/seed-manifest.json`、`d0.dump`：数据集索引与持久快照。
- `api/`、`jobs/s0/`、`telemetry/`：工具验证、逐任务明细和资源停止记录。

结束前数据库 `active_tasks=0`、`pending_outbox=0`。独立Compose项目已down，保留命名卷、宿主快照及镜像；原项目保持原状态。没有使用 `down -v`。

待其他实验结束、资源稳定后，在仓库根目录恢复：

```powershell
docker compose --env-file .env.perf -p asset-amqp-perf up -d --wait --scale task-worker=1
if ($LASTEXITCODE -ne 0) { throw 'Performance environment startup failed' }
pwsh -NoProfile -File scripts/perf/database.ps1 -Mode restore -Name d0 -OutDir data/perf/20261015-local-baseline
if ($LASTEXITCODE -ne 0) { throw 'D0 checkpoint restore failed' }

# 资源达标后重启后台采集，再重跑被中断的正式测试。
$telemetry = Join-Path (Get-Location) 'data/perf/20261015-local-baseline/telemetry'
if (Test-Path (Join-Path $telemetry 'collector.stop')) {
    Remove-Item -LiteralPath (Join-Path $telemetry 'collector.stop')
}
Start-Process -FilePath (Get-Command pwsh).Source -ArgumentList @('-NoProfile','-File','scripts/perf/collect.ps1','-EnvFile','.env.perf','-ComposeProject','asset-amqp-perf','-OutDir',$telemetry) -WindowStyle Hidden
# run.ps1本身也检查最低资源条件；旧的资源停止标记会在资源稳定后保留归档。
pwsh -NoProfile -File scripts/perf/run.ps1 -SeedManifest data/perf/20261015-local-baseline/seed-d0/seed-manifest.json -Mode read -Rates 5 -WarmupSeconds 60 -DurationSeconds 180 -RunPrefix s1-d0-resume -MaxVUs 32
```

后续按方案恢复相同快照再做对照；正式D1准备前应恢复empty快照。凭据保存在忽略的`.env.perf`和种子credentials文件中，报告没有收录口令或Token。
