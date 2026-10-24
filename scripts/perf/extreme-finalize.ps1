#requires -Version 7.2
param([string]$OutDir='data/perf/optimization-extreme-20261019-v2',[string]$DiagnosticDir='data/perf/optimization-extreme-20261019-client512',[string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot);Set-Location -LiteralPath $repo
$out=(Resolve-Path $OutDir).Path
if(Test-Path "$out/final-verification.json"){throw 'Preserve prior final verification'}
$lease=[IO.File]::Open((Join-Path $repo 'data/perf/.asset-amqp-opt-extreme.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
$state=@{status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');all_new_performance_goals_passed=$false}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed"}}
try {
    if((Get-Content "$out/state.json" -Raw|ConvertFrom-Json).status -ne 'completed'){throw 'Main experiment not complete'}
    if(-not(Get-Content "$DiagnosticDir/state.json" -Raw|ConvertFrom-Json).final_restore){throw 'Diagnostic restore not complete'}
    if(Get-Process k6 -ErrorAction SilentlyContinue){throw 'A load client is still running'}
    & node "$PSScriptRoot/preauth-contract-test.mjs" *> "$out/preauth-contract-final.log";Check 'Business and authentication contract'
    & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$out/permissions-final.json" *> "$out/permissions-final.log";Check '147 count permission assertions'
    $state.permission_checks=147
    $env:OPT_IMAGE_TAG='p2-observe';$env:WORKER_SLOTS='2'
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers 1 *> "$out/verified-final-restore.log";Check 'D1 restore after permission checks'
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -OutDir $out -EnvFile $EnvFile *> "$out/checkpoint-verified.log";Check 'Verified final checkpoint'
    $checkpoint=(Get-ChildItem $out -Directory -Filter 'checkpoint-*'|Sort-Object Name|Select-Object -Last 1).FullName
    & python "$PSScriptRoot/p3-source-proof.py" --checkpoint $checkpoint --output "$out/source-final.json" *> "$out/source-final.log";Check '110 application files and seven frozen image identities'
    $values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected database'}
    $sql="SELECT json_build_object('responsible_total',(SELECT count(*) FROM asset.responsible_department_assets),'responsible_active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions),'active_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $sql;Check 'D1 quiescence'
    $q=$raw|ConvertFrom-Json;$q|ConvertTo-Json|Set-Content "$out/quiescence-final.json"
    if($q.responsible_total -ne 40001 -or $q.responsible_active -ne 38010 -or $q.versions -ne 145002 -or $q.active_tasks -ne 0 -or $q.unpublished_events -ne 0){throw 'D1/quiescence mismatch'}
    & docker compose --env-file $EnvFile -p asset-amqp-opt exec -T rabbitmq rabbitmqctl list_queues -q name messages_ready messages_unacknowledged *> "$out/queues-final.tsv";Check 'Final queue evidence'
    $ids=@(& docker ps -q --filter label=com.docker.compose.project=asset-amqp-opt)
    $health=@(& docker inspect @ids --format '{"name":{{json .Name}},"health":{{json .State.Health.Status}}}'|ForEach-Object{$_|ConvertFrom-Json});Check 'Final health'
    $health|ConvertTo-Json|Set-Content "$out/health-final.json"
    if($health.Count -ne 11 -or @($health|Where-Object health -ne healthy).Count){throw 'Expected 11 healthy persistent containers'}
    $state.healthy_containers=$health.Count;$state.frozen_source_and_images=$true;$state.restored_d1=$true
    $state.active_k6_processes=@(Get-Process k6 -ErrorAction SilentlyContinue).Count
    $state.active_extreme_collectors=@(Get-CimInstance Win32_Process -Filter "Name='pwsh.exe'"|Where-Object{$_.CommandLine -match 'collect.ps1' -and $_.CommandLine -match 'optimization-extreme-'}).Count
    if($state.active_k6_processes -or $state.active_extreme_collectors){throw 'Residual pressure process'}
    $os=Get-CimInstance Win32_OperatingSystem;$state.final_host_free_ratio=[double]$os.FreePhysicalMemory/$os.TotalVisibleMemorySize
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
} catch {$state.status='failed';$state.error=$_.Exception.Message;throw}
finally {$state|ConvertTo-Json -Depth 8|Set-Content "$out/final-verification.json";Remove-Item Env:OPT_IMAGE_TAG,Env:WORKER_SLOTS -ErrorAction SilentlyContinue;$lease.Dispose()}
