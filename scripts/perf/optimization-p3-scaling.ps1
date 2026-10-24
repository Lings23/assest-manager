#requires -Version 7.2
# Four real imports with live traffic, after isolated functional/fault regression.
param([string]$EnvFile='.env.optimization',[switch]$Resume)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p3'
$path="$out/p3-four-controller.json"
if((Test-Path $path) -and -not $Resume){throw 'Preserve prior four-task experiment'}
$validation=Get-ChildItem $out -Directory -Filter 'final-validation-*'|Sort-Object Name|Select-Object -Last 1
if(-not $validation){throw 'First complete the independent final regression'}
$state=@{status='waiting-regression';started_utc=[DateTimeOffset]::UtcNow.ToString('o');regression=$validation.FullName;runs=@();error=$null}
if($Resume){
    $previous=Get-Content $path -Raw|ConvertFrom-Json
    if($previous.status -ne 'halted' -or $previous.runs.Count -ne 0 -or $previous.error -ne 'Independent regression failed'){throw 'Resume only the reviewed pre-work historical-DLQ gate'}
    $state.prior_controller=$previous
}
function Save {$state|ConvertTo-Json -Depth 14|Set-Content $path}
function Check([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; retain current backend before new work"}}
Save
$collector=$null;$priorImage=$env:OPT_IMAGE_TAG;$priorSlots=$env:WORKER_SLOTS
try {
    while($true){
        $check=Get-Content "$($validation.FullName)/state.json" -Raw|ConvertFrom-Json
        if($check.status -eq 'failed'){
            if((Test-Path "$($validation.FullName)/acceptance-recovery.json") -and (Get-Content "$($validation.FullName)/acceptance-recovery.json" -Raw|ConvertFrom-Json).status -eq 'completed'){break}
            throw 'Independent regression failed'
        }
        if($check.status -eq 'completed'){break}
        Start-Sleep -Seconds 5
    }
    if(Get-Process k6 -ErrorAction SilentlyContinue){throw 'Another pressure client is still active'}
    if(Test-Path "$out/telemetry/abort.json"){throw 'Resource abort requires investigation'}
    if(Test-Path "$out/telemetry/collector.stop"){Move-Item -LiteralPath "$out/telemetry/collector.stop" -Destination "$out/telemetry/collector.stop.p3-four"}
    $env:OPT_IMAGE_TAG='p2-observe';$env:WORKER_SLOTS='2'
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList @('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio','0.05') -WindowStyle Hidden -PassThru -RedirectStandardOutput "$out/p3-four-collector.stdout.log" -RedirectStandardError "$out/p3-four-collector.stderr.log"
    $state.collector_pid=$collector.Id;Save
    foreach($workers in @(1,2)){
        $prefix="p3-four-w$workers";$state.status="running-w$workers";Save
        & "$PSScriptRoot/interference.ps1" -OutDir $out -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -CandidatePath data/perf/20261016-local-baseline/api-20261017r2-candidate.json -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers $workers -Slots 2 -OnlineRPS 48 -FrozenSnapshotPath data/perf/20261016-local-baseline/s7base.dump -FrozenExportManifest data/perf/20261016-local-baseline/interference/s7-20261017/prepare-export/export-s7-20261017-prep-e.manifest.json -SnapshotName s7base -RunPrefix $prefix -Repetitions 1 -MaxVUs 64 -MinHostFreeMemoryRatio 0.05 -Points control,import -ImportConcurrency 4 *> "$out/$prefix.log"
        Check "Four-import experiment w$workers"
        $suite=Get-Content "$out/interference/$prefix/state.json" -Raw|ConvertFrom-Json
        if($suite.status -ne 'completed'){throw 'Four-task suite not completed'}
        $imports=@($suite.cases|Where-Object point -eq import)[0]
        $identities=@($imports.execution_evidence.worker_id|Select-Object -Unique)
        if($workers -eq 2 -and $identities.Count -lt 2){throw 'The four-task load still did not exercise two workers; no expansion claim'}
        & "$PSScriptRoot/worker-runtime-proof.ps1" -ExpectedWorkers $workers -ExpectedSlots 2 -Output "data/perf/optimization-p3/$prefix-runtime.json"
        $state.runs+=@{workers=$workers;slots=2;imports=4;rows_per_task=100000;state="$out/interference/$prefix/state.json";actual_worker_ids=$identities};Save
    }
    'Four-task measurements finished'|Set-Content "$out/telemetry/collector.stop"
    Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue
    $state.status='restoring-final-single-instance';Save
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers 1 *> "$out/post-scaling-restore.log";Check 'Post-scaling D1 restore'
    & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$out/post-scaling-permissions.json" *> "$out/post-scaling-permissions.log";Check 'Post-scaling 147 permissions'
    & "$PSScriptRoot/worker-runtime-proof.ps1" -ExpectedWorkers 1 -ExpectedSlots 2 -Output data/perf/optimization-p3/final-single-runtime.json
    & "$PSScriptRoot/checkpoint.ps1" -Stage p3 -EnvFile $EnvFile *> "$out/post-scaling-checkpoint.log";Check 'Post-scaling checkpoint'
    $checkpoint=Get-ChildItem $out -Directory -Filter 'checkpoint-*'|Sort-Object Name|Select-Object -Last 1
    & python "$PSScriptRoot/p3-source-proof.py" --checkpoint $checkpoint.FullName --output "$out/post-scaling-source-proof.json" *> "$out/post-scaling-source-proof.log";Check 'Unchanged frozen application'
    $values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    $sql="SELECT json_build_object('responsible_total',(SELECT count(*) FROM asset.responsible_department_assets),'responsible_active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions),'active_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $sql;Check 'Post-scaling SQL quiescence'
    $audit=$raw|ConvertFrom-Json;$audit|ConvertTo-Json|Set-Content "$out/post-scaling-quiescence.json"
    if($audit.responsible_total -ne 40001 -or $audit.responsible_active -ne 38010 -or $audit.versions -ne 145002 -or $audit.active_tasks -ne 0 -or $audit.unpublished_events -ne 0){throw 'Final restored D1 differs'}
    $first=Get-Content "$out/telemetry/rabbitmq.jsonl" -TotalCount 1|ConvertFrom-Json
    $basic=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($values.RABBITMQ_USER+':'+$values.RABBITMQ_PASSWORD))
    $queues=Invoke-RestMethod -Uri ('http://127.0.0.1:'+$values.RABBITMQ_MANAGEMENT_PORT+'/api/queues') -Headers @{Authorization="Basic $basic"} -TimeoutSec 10
    $queueProof=@(foreach($queue in $queues){
        $original=@($first.queues|Where-Object name -eq $queue.name)
        $expected=if($queue.name.EndsWith('.dead') -and $original.Count -eq 1){[long]$original[0].messages_ready}else{0}
        if([long]$queue.messages_ready -ne $expected -or [long]$queue.messages_unacknowledged -ne 0){throw 'Post-scaling main queue/DLQ audit failed'}
        @{name=$queue.name;baseline=$expected;ready=$queue.messages_ready;unacked=$queue.messages_unacknowledged;delta=[long]$queue.messages_ready-$expected}
    })
    $queueProof|ConvertTo-Json -Depth 5|Set-Content "$out/post-scaling-queues.json"
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save
}catch{$state.status='halted';$state.error=$_.Exception.Message;Save;throw}
finally{
    if($collector){'Controller finished'|Set-Content "$out/telemetry/collector.stop";Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue}
    [Environment]::SetEnvironmentVariable('OPT_IMAGE_TAG',$priorImage,'Process');[Environment]::SetEnvironmentVariable('WORKER_SLOTS',$priorSlots,'Process')
}
