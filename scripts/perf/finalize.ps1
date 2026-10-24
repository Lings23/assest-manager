#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/20261016-local-baseline',
    [string]$EnvFile='.env.perf',
    [string]$ApiPrefix='api-20261017',
    [ValidateSet('All','Remaining')][string]$ApiStage='All',
    [string]$InterferencePrefix='s7-20261017',
    [string]$CorrectnessRunId='s10-20261017'
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$OutDir=[IO.Path]::GetFullPath($OutDir)
$allowed=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $OutDir.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'OutDir must be inside ignored data/perf'}
foreach($value in @($ApiPrefix,$InterferencePrefix,$CorrectnessRunId)){if($value -notmatch '^[a-zA-Z0-9_-]{1,30}$'){throw 'Invalid run identifier'}}
$apiPath=Join-Path $OutDir "$ApiPrefix-$ApiStage-status.json"
$s7Path=Join-Path $OutDir "interference/$InterferencePrefix/state.json"
$s10Path=Join-Path $OutDir "jobs/$CorrectnessRunId/correctness-$CorrectnessRunId.manifest.json"
$statusPath=Join-Path $OutDir 'finalization-status.json'
$state=@{status='waiting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');application_project='asset-amqp-perf';error=$null}
function Save-State {$state|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $statusPath -Encoding utf8}
function Read-State([string]$Path){if(Test-Path -LiteralPath $Path){try{return Get-Content -LiteralPath $Path -Raw|ConvertFrom-Json}catch{}};return $null}
Save-State
try{
    while($true){
        if(Test-Path -LiteralPath (Join-Path $OutDir 'telemetry/abort.json')){throw 'Resource abort requires inspection before finalization'}
        $api=Read-State $apiPath;$s7=Read-State $s7Path;$s10=Read-State $s10Path
        if($api.status -eq 'interrupted' -or $s7.status -eq 'halted'){throw 'A scenario interrupted; preserve the environment for inspection'}
        $kinds=@($s10.batches.kind)
        if($kinds -contains 'concurrent_samekey' -and $kinds -contains 'concurrent_sameversion'){
            if(@($s10.batches|Where-Object{-not $_.passed}).Count -or $s10.failed_or_unfinished -ne 0){throw 'Concurrency correctness failed'}
            if($api.status -ne 'completed' -or $s7.status -ne 'completed'){throw 'Dependent scenarios are not complete'}
            break
        }
        Start-Sleep -Seconds 5
    }
    $state.status='finalizing';$state.tests_finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
    $state.api_cases_with_failed_points=@($api.completed|Where-Object{$_.failed_points -gt 0})
    $state.note='Execution finished is separate from passing performance thresholds; review observed-results and sample qualifications.'
    Save-State
    'Required load scenarios finished; stopping collector before archival'|Set-Content -LiteralPath (Join-Path $OutDir 'telemetry/collector.stop')
    $pidFile=Join-Path $OutDir 'telemetry/pid.txt'
    if(Test-Path -LiteralPath $pidFile){
        $collectorProcessId=[int](Get-Content -LiteralPath $pidFile)
        $collector=Get-Process -Id $collectorProcessId -ErrorAction SilentlyContinue
        if($collector -and -not $collector.WaitForExit(30000)){throw 'Collector did not stop; no containers were removed'}
    }
    $config=@{}
    Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$config[$matches[1]]=$matches[2]}}
    if($config.POSTGRES_DB -ne 'asset_perf' -or $config.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database configuration'}
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-perf exec -T postgres psql -U $config.POSTGRES_USER -d $config.POSTGRES_DB -Atc "SELECT json_build_object('active_dispatched_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    if($LASTEXITCODE -ne 0){throw 'Final database observation failed'}
    $quiet=$raw|ConvertFrom-Json
    $quiet|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $OutDir 'final-database-state.json')
    if($quiet.active_dispatched_tasks -ne 0 -or $quiet.unpublished_events -ne 0){throw 'Outstanding database work; no teardown is permitted'}
    if($config.RABBITMQ_MANAGEMENT_PORT -ne '59673'){throw 'Unexpected isolated RabbitMQ management port'}
    $basic=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($config.RABBITMQ_USER+':'+$config.RABBITMQ_PASSWORD))
    $deadline=[DateTimeOffset]::UtcNow.AddSeconds(30)
    do{
        $queueResponse=Invoke-RestMethod -Uri 'http://127.0.0.1:59673/api/queues' -Headers @{Authorization="Basic $basic"} -TimeoutSec 3
        $queues=@($queueResponse)
        $work=@($queues|Where-Object{$_.name -notmatch '\.dead$'})
        $busy=@($work|Where-Object{$null -eq $_.messages_ready -or $null -eq $_.messages_unacknowledged -or $_.messages_ready -ne 0 -or $_.messages_unacknowledged -ne 0})
        if($work.Count -gt 0 -and $busy.Count -eq 0){break}
        if([DateTimeOffset]::UtcNow -ge $deadline){throw 'Work queue did not drain; no teardown is permitted'}
        Start-Sleep -Seconds 2
    }while($true)
    @($queues|Select-Object name,vhost,messages_ready,messages_unacknowledged,consumers)|ConvertTo-Json -Depth 4|Set-Content -LiteralPath (Join-Path $OutDir 'final-rabbitmq-state.json')
    $snapshotName='final-'+$CorrectnessRunId
    if(Test-Path -LiteralPath (Join-Path $OutDir "$snapshotName.dump")){throw 'Final snapshot already exists; inspect the earlier finalization before overwriting evidence'}
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode snapshot -Name $snapshotName -OutDir $OutDir -EnvFile $EnvFile *> (Join-Path $OutDir 'final-snapshot.log')
    if(-not $?){throw 'Final snapshot failed'}
    & (Join-Path $PSScriptRoot 'summarize.ps1') -ResultsDir $OutDir -OutputJson (Join-Path $OutDir 'observed-results.json') -OutputMarkdown (Join-Path $OutDir 'observed-results.md') *> (Join-Path $OutDir 'summarize.log')
    if(-not $?){throw 'Result summary failed'}
    & python -B (Join-Path $PSScriptRoot 'resources-summary.py') --results-dir $OutDir --output-json (Join-Path $OutDir 'resource-observed-results.json') *> (Join-Path $OutDir 'resource-summary.log')
    if($LASTEXITCODE -ne 0){throw 'Resource summary failed'}
    & docker compose --env-file $EnvFile -p asset-amqp-perf ps -a --format json *> (Join-Path $OutDir 'containers-final.jsonl')
    if($LASTEXITCODE -ne 0){throw 'Final container archival failed'}
    & docker compose --env-file $EnvFile -p asset-amqp-perf logs --no-color --no-log-prefix *> (Join-Path $OutDir 'services-final.log')
    if($LASTEXITCODE -ne 0){throw 'Service log archival failed'}
    & docker compose --env-file $EnvFile -p asset-amqp-perf down *> (Join-Path $OutDir 'teardown.log')
    if($LASTEXITCODE -ne 0){throw 'Isolated project teardown failed'}
    $state.status='archived';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');$state.named_volumes_preserved=$true
    Save-State
    Write-Output "Load results archived; isolated project stopped without deleting volumes: $statusPath"
}catch{$state.status='halted';$state.error=$_.Exception.Message;$state.halted_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State;throw}
