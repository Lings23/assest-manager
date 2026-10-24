#requires -Version 7.2
# Save reproducibility metadata without dumping environment secrets.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('stage0','p0','p1-sql','p1-import','p2','p3')][string]$Stage,
    [string]$EnvFile='.env.optimization',
    [string]$OutDir=''
)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=if($OutDir){[IO.Path]::GetFullPath($(if([IO.Path]::IsPathRooted($OutDir)){$OutDir}else{Join-Path $repo $OutDir}))}else{Join-Path $repo "data/perf/optimization-$Stage"}
$allowed=[IO.Path]::GetFullPath((Join-Path $repo 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $out.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'Checkpoint archive must be inside data/perf'}
New-Item -ItemType Directory -Force -Path $out|Out-Null
$dest=Join-Path $out ('checkpoint-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
New-Item -ItemType Directory -Path $dest|Out-Null
$values=@{}
Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated optimization database'}
$compose=@('compose','--env-file',$EnvFile,'-p','asset-amqp-opt')
$paths=@(rg --files platform scripts/perf docs/testing deploy compose.yaml .env.example | Where-Object{$_ -notmatch '__pycache__|\.pyc$'})
@(foreach($p in $paths){@{path=$p;sha256=(Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant()}})|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dest 'source-hashes.json') -Encoding utf8
git status --porcelain=v1|Set-Content -LiteralPath (Join-Path $dest 'worktree.txt')
git rev-parse HEAD|Set-Content -LiteralPath (Join-Path $dest 'git-head.txt')
$ids=@(& docker ps -aq --filter label=com.docker.compose.project=asset-amqp-opt)
if($LASTEXITCODE -ne 0 -or -not $ids.Count){throw 'Optimization containers unavailable'}
& docker inspect @ids --format '{"name":{{json .Name}},"image_id":{{json .Image}},"image":{{json .Config.Image}},"state":{{json .State.Status}},"cpu_quota":{{json .HostConfig.CpuQuota}},"cpu_period":{{json .HostConfig.CpuPeriod}},"nano_cpus":{{json .HostConfig.NanoCpus}},"memory":{{json .HostConfig.Memory}},"memory_swap":{{json .HostConfig.MemorySwap}}}'|Set-Content -LiteralPath (Join-Path $dest 'containers.jsonl')
$settings="SELECT json_build_object('version',version(),'settings',(SELECT json_object_agg(name,setting) FROM pg_settings WHERE name IN ('shared_buffers','work_mem','effective_cache_size','random_page_cost','max_connections','jit','max_parallel_workers_per_gather','track_io_timing','shared_preload_libraries')),'asset_indexes',(SELECT json_agg(row_to_json(i)) FROM (SELECT tablename,indexname,indexdef,pg_relation_size((schemaname||'.'||indexname)::regclass) bytes FROM pg_indexes WHERE schemaname='asset' ORDER BY tablename,indexname) i),'asset_table_statistics',(SELECT json_agg(row_to_json(s)) FROM (SELECT t.relname,t.n_live_tup,t.n_dead_tup,t.last_analyze,t.last_autoanalyze,c.reltuples,c.relpages FROM pg_stat_all_tables t JOIN pg_class c ON c.oid=t.relid WHERE t.schemaname='asset' ORDER BY t.relname) s));"
& docker @compose exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $settings|Set-Content -LiteralPath (Join-Path $dest 'postgres.json')
if($LASTEXITCODE -ne 0){throw 'PostgreSQL metadata failed'}
$config=[ordered]@{}
foreach($key in @('POSTGRES_DB','POSTGRES_PORT','POSTGRES_USER','GATEWAY_PORT','WEB_PORT','RABBITMQ_PORT','RABBITMQ_MANAGEMENT_PORT','MINIO_PORT','MINIO_CONSOLE_PORT','S3_BUCKET','WORKER_SLOTS','WORKER_LEASE_DURATION','WORKER_RENEW_INTERVAL','WORKER_TASK_TIMEOUT','WORKER_RECOVERY_INTERVAL','WORKER_DRAIN_TIMEOUT')){
    $config[$key]=if($values.ContainsKey($key)){$values[$key]}else{$null}
}
$os=Get-CimInstance Win32_OperatingSystem
$info=& docker info --format '{{json .}}'|ConvertFrom-Json
$meta=@{utc=[DateTimeOffset]::UtcNow.ToString('o');stage=$Stage;project='asset-amqp-opt';configuration=$config;host_total_bytes=[long]$os.TotalVisibleMemorySize*1024;host_free_bytes=[long]$os.FreePhysicalMemory*1024;docker_cpus=$info.NCPU;docker_memory_bytes=$info.MemTotal;docker_version=$info.ServerVersion;compose_version=(& docker compose version --short);go_version=(& go version);d1_sha256=(Get-FileHash data/perf/20261016-local-baseline/d1.dump).Hash;limitations='Pool maximum and runtime/broker gauges are captured separately. No secret values or access tokens are stored here.'}
$meta|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $dest 'metadata.json') -Encoding utf8
Write-Output "Checkpoint saved: $dest"
