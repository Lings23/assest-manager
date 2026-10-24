param(
    [string]$EnvFile = '.env.perf',
    [string]$ComposeProject = 'asset-amqp-perf',
    [Parameter(Mandatory)][string]$OutDir,
    [int]$IntervalSeconds = 5,
    [switch]$Enhanced,
    [ValidateRange(0.01,0.5)][double]$MinHostFreeMemoryRatio=0.15
)
$ErrorActionPreference = 'Stop'
if (-not $ComposeProject.StartsWith('asset-amqp-')) { throw 'Collector requires an isolated asset-amqp-* project' }
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$null = New-Item -ItemType Directory -Path $OutDir -Force
$OutDir = (Resolve-Path -LiteralPath $OutDir).Path
$stopPath = Join-Path $OutDir 'collector.stop'
$abortPath = Join-Path $OutDir 'abort.json'
$composeArgs = @('compose', '--env-file', $EnvFile, '-p', $ComposeProject)
$envValues = @{}
Get-Content -LiteralPath $EnvFile | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $envValues[$matches[1]] = $matches[2] } }
$databaseUser = $envValues.POSTGRES_USER
$databaseName = $envValues.POSTGRES_DB
$sample = 0
$memoryPressureSince = $null
$dockerMemoryPressureSince = $null
$previousHostProcesses=@{}
$servicePorts=@{'gateway'=8080;'iam-service'=8081;'asset-service'=8082;'governance-service'=8083;'task-report-service'=8084;'task-publisher'=8085;'task-worker'=8086}
function Write-Observation([string]$File, $Value) {
    $Value | ConvertTo-Json -Depth 12 -Compress | Add-Content -LiteralPath (Join-Path $OutDir $File) -Encoding utf8
}
function Stop-NewLoad([string]$Reason) {
    if (-not (Test-Path -LiteralPath $abortPath)) {
        [pscustomobject]@{ utc = [DateTimeOffset]::UtcNow.ToString('o'); reason = $Reason } |
            ConvertTo-Json | Set-Content -LiteralPath $abortPath -Encoding utf8
    }
}
Write-Observation 'collector-configuration.jsonl' @{utc=[DateTimeOffset]::UtcNow.ToString('o');enhanced=[bool]$Enhanced;interval_seconds=$IntervalSeconds;script_sha256=(Get-FileHash -LiteralPath $PSCommandPath).Hash;min_host_free_memory_ratio=$MinHostFreeMemoryRatio;resource_guards="Host free below$($MinHostFreeMemoryRatio*100)% for30s; Docker available <=10% for30s; disk below10GiB";metrics_access='Container loopback only; no scrape tokens are written'}
while (-not (Test-Path -LiteralPath $stopPath)) {
    $tick = [Diagnostics.Stopwatch]::StartNew()
    $timestamp = [DateTimeOffset]::UtcNow.ToString('o')
    try {
        $containers = @(& docker ps -a --filter "label=com.docker.compose.project=$ComposeProject" --format '{"ID":{{json .ID}},"Names":{{json .Names}},"State":{{json .State}},"Status":{{json .Status}}}' |
            ForEach-Object { $_ | ConvertFrom-Json })
        if ($LASTEXITCODE -ne 0) { throw 'docker ps failed' }
        $ids = @($containers | Where-Object State -eq 'running' | ForEach-Object ID)
        $stats = if ($ids.Count) { @(& docker stats --no-stream --format '{{json .}}' @ids | ForEach-Object { $_ | ConvertFrom-Json }) } else { @() }
        Write-Observation 'resources.jsonl' @{ utc = $timestamp; containers = $containers; stats = $stats }
        $unexpected = @($containers | Where-Object { $_.State -in @('exited','dead','restarting') -and $_.Names -notmatch '-task-migrate-' })
        if ($unexpected.Count -and -not (Test-Path -LiteralPath (Join-Path $OutDir 'maintenance.flag'))) { Stop-NewLoad ('Unexpected container state: ' + ($unexpected.Names -join ',')) }
        $processes = @(Get-Process -Name k6,pwsh -ErrorAction SilentlyContinue |
            Select-Object Id,ProcessName,CPU,WorkingSet64,PrivateMemorySize64,Threads)
        Write-Observation 'load-processes.jsonl' @{ utc = $timestamp; processes = @($processes | ForEach-Object {
            @{ id=$_.Id; name=$_.ProcessName; cpu_seconds=$_.CPU; rss_bytes=$_.WorkingSet64; private_bytes=$_.PrivateMemorySize64; threads=$_.Threads.Count }
        }) }
        if ($sample % 3 -eq 0) {
            $os = Get-CimInstance Win32_OperatingSystem
            $hostCpuPercent=$null
            try { $hostCpuPercent=(Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor -Filter "Name='_Total'").PercentProcessorTime } catch { }
            $hostFreeRatio = [double]$os.FreePhysicalMemory / [double]$os.TotalVisibleMemorySize
            $disk = Get-PSDrive -Name D
            $dockerUsage = & docker exec "$ComposeProject-postgres-1" sh -c 'cat /proc/meminfo | head -n 3'
            $dockerMemory=@{}
            foreach($line in $dockerUsage){if($line -match '^(MemTotal|MemAvailable):\s+(\d+)\s+kB'){$dockerMemory[$matches[1]]=[double]$matches[2]}}
            $dockerAvailableRatio=if($dockerMemory.MemTotal -gt 0 -and $dockerMemory.ContainsKey('MemAvailable')){$dockerMemory.MemAvailable/$dockerMemory.MemTotal}else{$null}
            Write-Observation 'host-resources.jsonl' @{ utc=$timestamp; cpu_percent=$hostCpuPercent; free_memory_ratio=$hostFreeRatio; free_memory_kib=$os.FreePhysicalMemory; total_memory_kib=$os.TotalVisibleMemorySize; disk_free_bytes=$disk.Free; docker_memory=$dockerUsage; docker_available_memory_ratio=$dockerAvailableRatio }
            if ($hostFreeRatio -lt $MinHostFreeMemoryRatio) {
                if (-not $memoryPressureSince) { $memoryPressureSince=[DateTimeOffset]::UtcNow }
                if (([DateTimeOffset]::UtcNow-$memoryPressureSince).TotalSeconds -ge 30) { Stop-NewLoad "Host free memory below $($MinHostFreeMemoryRatio*100)% for 30 seconds" }
            } else { $memoryPressureSince=$null }
            if($null -ne $dockerAvailableRatio -and $dockerAvailableRatio -le 0.10){
                if(-not $dockerMemoryPressureSince){$dockerMemoryPressureSince=[DateTimeOffset]::UtcNow}
                if(([DateTimeOffset]::UtcNow-$dockerMemoryPressureSince).TotalSeconds -ge 30){Stop-NewLoad 'Docker available memory at or below 10% for 30 seconds'}
            }else{$dockerMemoryPressureSince=$null}
            if ($disk.Free -lt 10GB) { Stop-NewLoad 'D drive free space below 10 GiB' }
            if($Enhanced){
                $currentHostProcesses=@{};$inventory=[Collections.Generic.List[object]]::new()
                foreach($process in @(Get-Process -ErrorAction SilentlyContinue)){
                    try {
                        $created=$process.StartTime.ToUniversalTime().Ticks
                        $identity="$($process.Id):$created"
                        $cpuValue=$process.CPU
                        if($null -eq $cpuValue){continue}
                        $cpu=[double]$cpuValue;$now=[DateTimeOffset]::UtcNow
                        $previous=$previousHostProcesses[$identity]
                        $delta=if($previous -and $now -gt $previous.utc){100*($cpu-$previous.cpu)/($now-$previous.utc).TotalSeconds}else{$null}
                        $currentHostProcesses[$identity]=@{utc=$now;cpu=$cpu}
                        $inventory.Add(@{id=$process.Id;name=$process.ProcessName;cpu_seconds=$cpu;sample_cpu_percent_one_core=$delta;rss_bytes=$process.WorkingSet64;private_bytes=$process.PrivateMemorySize64})
                    }catch{}
                }
                $previousHostProcesses=$currentHostProcesses
                $selected=@(@($inventory|Sort-Object sample_cpu_percent_one_core -Descending|Select-Object -First 12)+@($inventory|Sort-Object rss_bytes -Descending|Select-Object -First 12)|Sort-Object id -Unique)
                Write-Observation 'host-processes.jsonl' @{utc=$timestamp;processes=$selected;definition='Cumulative process CPU deltas; one core=100%. New/protected process CPU is unavailable, not zero. No command lines are collected.'}
                foreach($container in @($containers|Where-Object State -eq 'running')){
                    if($container.Names -notmatch ('^'+[regex]::Escape($ComposeProject)+'-(.+)-\d+$')){continue}
                    $service=$matches[1]
                    if(-not $servicePorts.ContainsKey($service)){continue}
                    $watch=[Diagnostics.Stopwatch]::StartNew()
                    $metrics=& docker exec $container.ID wget -T 3 -qO- "http://127.0.0.1:$($servicePorts[$service])/metrics/service" 2>&1
                    $metricExit=$LASTEXITCODE
                    Write-Observation 'service-metrics.jsonl' @{utc=[DateTimeOffset]::UtcNow.ToString('o');instance=$container.Names;service=$service;exit_code=$metricExit;duration_ms=$watch.Elapsed.TotalMilliseconds;text=($metrics -join "`n")}
                }
            }
            try {
                $rabbitPort=if($envValues.RABBITMQ_MANAGEMENT_PORT){$envValues.RABBITMQ_MANAGEMENT_PORT}else{'15672'}
                $rabbitUser=if($envValues.RABBITMQ_DEFAULT_USER){$envValues.RABBITMQ_DEFAULT_USER}else{$envValues.RABBITMQ_USER}
                $rabbitPassword=if($envValues.RABBITMQ_DEFAULT_PASS){$envValues.RABBITMQ_DEFAULT_PASS}else{$envValues.RABBITMQ_PASSWORD}
                $basic=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($rabbitUser+':'+$rabbitPassword))
                $queues=Invoke-RestMethod -Uri "http://127.0.0.1:$rabbitPort/api/queues" -Headers @{Authorization="Basic $basic"} -TimeoutSec 3
                Write-Observation 'rabbitmq.jsonl' @{utc=$timestamp;queues=@($queues|ForEach-Object{@{name=$_.name;vhost=$_.vhost;messages_ready=$_.messages_ready;messages_unacknowledged=$_.messages_unacknowledged;consumers=$_.consumers;publish_rate=$_.message_stats.publish_details.rate;ack_rate=$_.message_stats.ack_details.rate}})}
            }catch{Write-Observation 'collector-errors.jsonl' @{utc=$timestamp;component='rabbitmq';error=$_.Exception.Message}}
            foreach ($container in @($containers | Where-Object { $_.State -eq 'running' -and $_.Names -match '-task-(publisher|worker)-' })) {
                $port = if ($container.Names -match '-task-publisher-') { 8085 } else { 8086 }
                $metrics = & docker exec $container.ID wget -qO- "http://127.0.0.1:$port/metrics" 2>&1
                Write-Observation 'task-metrics.jsonl' @{ utc=$timestamp; instance=$container.Names; exit_code=$LASTEXITCODE; text=($metrics -join "`n") }
            }
            $query = "SELECT json_build_object('active',count(*) FILTER (WHERE state='active'),'waiting',count(*) FILTER (WHERE wait_event IS NOT NULL),'locks',count(*) FILTER (WHERE wait_event_type='Lock'),'longest_seconds',COALESCE(max(EXTRACT(EPOCH FROM clock_timestamp()-query_start)) FILTER (WHERE state='active'),0)) FROM pg_stat_activity WHERE datname=current_database(); SELECT json_build_object('active_tasks',count(*) FILTER (WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'dead',count(*) FILTER (WHERE state='dead_letter')) FROM task_report.tasks; SELECT json_build_object('stats_reset',stats_reset,'commits',xact_commit,'rollbacks',xact_rollback,'blocks_read',blks_read,'blocks_hit',blks_hit,'deadlocks',deadlocks,'temp_bytes',temp_bytes) FROM pg_stat_database WHERE datname=current_database();"
            $dbStats = & docker @composeArgs exec -T postgres psql -U $databaseUser -d $databaseName -Atc $query 2>&1
            Write-Observation 'database.jsonl' @{ utc=$timestamp; exit_code=$LASTEXITCODE; text=($dbStats -join "`n") }
            if($Enhanced){
                $detailQuery="SELECT json_build_object('activity',(SELECT COALESCE(json_agg(row_to_json(a)),'[]'::json) FROM (SELECT pid,state,wait_event_type,wait_event,EXTRACT(EPOCH FROM clock_timestamp()-query_start) AS query_age_seconds,EXTRACT(EPOCH FROM clock_timestamp()-xact_start) AS transaction_age_seconds,md5(query) AS query_fingerprint,pg_blocking_pids(pid) AS blockers FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND (state='active' OR state LIKE 'idle in transaction%' OR wait_event_type='Lock') ORDER BY pid) a),'summary',(SELECT json_build_object('active',count(*) FILTER(WHERE state='active'),'active_waiting',count(*) FILTER(WHERE state='active' AND wait_event IS NOT NULL),'active_lock_waiting',count(*) FILTER(WHERE state='active' AND wait_event_type='Lock'),'idle_in_transaction',count(*) FILTER(WHERE state LIKE 'idle in transaction%')) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()),'task_retry_attempts',(SELECT COALESCE(sum(GREATEST(attempt-1,0)),0) FROM task_report.tasks),'wal',(SELECT row_to_json(w) FROM pg_stat_wal w),'io',(SELECT json_agg(row_to_json(i)) FROM pg_stat_io i),'settings',(SELECT json_object_agg(name,setting) FROM pg_settings WHERE name IN ('track_io_timing','track_wal_io_timing','max_connections')));"
                $details=& docker @composeArgs exec -T postgres psql -X -U $databaseUser -d $databaseName -Atc $detailQuery 2>&1
                Write-Observation 'database-details.jsonl' @{utc=[DateTimeOffset]::UtcNow.ToString('o');exit_code=$LASTEXITCODE;text=($details -join "`n");definition='Active/idle-in-transaction only; idle ClientRead is not treated as lock or active waiting. SQL arguments/text are omitted.'}
            }
        }
    } catch {
        Write-Observation 'collector-errors.jsonl' @{ utc=$timestamp; error=$_.Exception.Message }
    }
    $sample++
    $sleepMs = [Math]::Max(100, $IntervalSeconds*1000-$tick.ElapsedMilliseconds)
    Start-Sleep -Milliseconds $sleepMs
}
