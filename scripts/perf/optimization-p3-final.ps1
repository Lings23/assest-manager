#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p3'
foreach($file in Get-ChildItem $out -Filter '*-controller.json'){
    $controller=Get-Content $file -Raw|ConvertFrom-Json
    if($controller.status -ne 'completed'){throw 'Finish or investigate every P3 controller before final acceptance'}
}
if(Get-Process k6 -ErrorAction SilentlyContinue){throw 'No live pressure clients may overlap acceptance'}
$dest=Join-Path $out ('final-validation-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
$null=New-Item -ItemType Directory -Path $dest
$state=@{status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');steps=@{}}
function Save {$state|ConvertTo-Json -Depth 12|Set-Content "$dest/state.json"}
function Check([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; preserve evidence before further work"}}
function Restore([string]$Label){
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers 1 *> "$dest/restore-$Label.log"
    Check "Restore $Label"
}
$keys=@('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL','OPT_IMAGE_TAG','WORKER_SLOTS')
$prior=@{};foreach($key in $keys){$prior[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
Save
try {
    $env:OPT_IMAGE_TAG='p2-observe';$env:WORKER_SLOTS='2'
    Restore 'initial'
    $values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database'}
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    foreach($key in @('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL')){[Environment]::SetEnvironmentVariable($key,$dsn+'asset_opt_test_p0?sslmode=disable','Process')}
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($values.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($values.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
    $goTool=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    Push-Location platform
    try{& $goTool test ./... -v -count=1 -timeout 12m *> "$dest/functional.log";Check 'Full real PostgreSQL/AMQP regression'}finally{Pop-Location}
    $state.steps.functional=$true;Save
    & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$dest/permissions.json" *> "$dest/permissions.log";Check '147 count permissions'
    & python "$PSScriptRoot/asset-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir "$dest/assets" *> "$dest/assets.log";Check 'Seven type CRUD/scope/version/concurrency'
    $state.steps.assets_and_permissions=$true;Save
    Restore 'before-exports'
    & python "$PSScriptRoot/export-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir "$dest/exports" *> "$dest/exports.log";Check 'Seven type CSV/download ownership'
    $state.steps.exports=$true;Save
    Restore 'before-fault'
    & python "$PSScriptRoot/wide-import.py" --env-file $EnvFile --out-dir "$dest/worker-fault" --run-id p3-final-worker-fault --case ordinary --rows 100000 --fault-worker --duplicate-deliveries 2 *> "$dest/worker-fault.log";Check 'Actual worker SIGKILL/duplicate delivery/partial-commit recovery'
    $state.steps.worker_fault=$true;Save
    Restore 'before-wide'
    & python "$PSScriptRoot/wide-import.py" --env-file $EnvFile --out-dir "$dest/wide-1000" --run-id p3-final-wide-1000 --case wide --rows 1000 *> "$dest/wide-1000.log";Check 'Wide record dynamic batching'
    $state.steps.wide=$true;Save
    Restore 'final'
    & "$PSScriptRoot/checkpoint.ps1" -Stage p3 -EnvFile $EnvFile *> "$dest/checkpoint.log";Check 'Final checkpoint'
    $checkpoint=Get-ChildItem $out -Directory -Filter 'checkpoint-*'|Sort-Object Name|Select-Object -Last 1
    & python "$PSScriptRoot/p3-source-proof.py" --checkpoint $checkpoint.FullName --output "$dest/frozen-source-proof.json" *> "$dest/frozen-source-proof.log";Check 'Frozen P2 source/image and original API/snapshot identity'
    $sql="SELECT json_build_object('responsible_total',(SELECT count(*) FROM asset.responsible_department_assets),'responsible_active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions),'active_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $sql;Check 'Final quiescence'
    $audit=$raw|ConvertFrom-Json;$audit|ConvertTo-Json|Set-Content "$dest/quiescence.json"
    if($audit.responsible_total -ne 40001 -or $audit.responsible_active -ne 38010 -or $audit.versions -ne 145002 -or $audit.active_tasks -ne 0 -or $audit.unpublished_events -ne 0){throw 'D1/quiescence mismatch'}
    $workerIds=@(& docker ps -q --filter label=com.docker.compose.project=asset-amqp-opt --filter label=com.docker.compose.service=task-worker)
    if($workerIds.Count -ne 1){throw 'Final deployment must contain one worker'}
    $slots=& docker exec $workerIds[0] printenv WORKER_SLOTS;Check 'Final worker slots'
    if($slots.Trim() -ne '2'){throw 'Final worker must have two slots'}
    $basic=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($values.RABBITMQ_USER+':'+$values.RABBITMQ_PASSWORD))
    $queues=Invoke-RestMethod -Uri ('http://127.0.0.1:'+$values.RABBITMQ_MANAGEMENT_PORT+'/api/queues') -Headers @{Authorization="Basic $basic"} -TimeoutSec 10
    $queueProof=@($queues|ForEach-Object{@{name=$_.name;ready=$_.messages_ready;unacked=$_.messages_unacknowledged;consumers=$_.consumers}})
    $queueProof|ConvertTo-Json -Depth 4|Set-Content "$dest/rabbitmq-queues.json"
    $baselineQueues=(Get-Content "$out/telemetry/rabbitmq.jsonl" -TotalCount 1|ConvertFrom-Json).queues
    foreach($queue in $queueProof){
        $old=@($baselineQueues|Where-Object name -eq $queue.name)
        $expected=if($queue.name.EndsWith('.dead') -and $old.Count -eq 1){[long]$old[0].messages_ready}else{0}
        if([long]$queue.ready -ne $expected -or [long]$queue.unacked -ne 0){throw 'Main queue must drain and historical dead-letter counts must not increase or be purged'}
    }
    Compress-Archive -Path platform,scripts/perf,compose.yaml,deploy/test/compose.optimization.yaml -DestinationPath "$dest/source-final.zip"
    $state.steps.final_restore=$true;$state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save
    Write-Output "Final verification saved: $dest"
}catch{$state.status='failed';$state.error=$_.Exception.Message;Save;throw}
finally{foreach($key in $keys){[Environment]::SetEnvironmentVariable($key,$prior[$key],'Process')}}
