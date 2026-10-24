# Read-only recovery of an overstrict historical-DLQ acceptance gate.
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p3'
$dest=(Get-ChildItem $out -Directory -Filter 'final-validation-*'|Sort-Object Name|Select-Object -Last 1).FullName
$old=Get-Content "$dest/state.json" -Raw|ConvertFrom-Json
if($old.status -ne 'failed' -or $old.error -ne 'Final isolated broker still has queued or unacked messages'){throw 'Only the reviewed historical-DLQ gate may be recovered'}
foreach($step in @('functional','exports','assets_and_permissions','wide','worker_fault')){if(-not $old.steps.$step){throw 'A required functional step did not pass'}}
if(Test-Path "$dest/acceptance-recovery.json"){throw 'Preserve previous recovery'}
$values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
$first=Get-Content "$out/telemetry/rabbitmq.jsonl" -TotalCount 1|ConvertFrom-Json
$basic=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($values.RABBITMQ_USER+':'+$values.RABBITMQ_PASSWORD))
$queues=Invoke-RestMethod -Uri ('http://127.0.0.1:'+$values.RABBITMQ_MANAGEMENT_PORT+'/api/queues') -Headers @{Authorization="Basic $basic"} -TimeoutSec 10
$proof=@(foreach($queue in $queues){
    $baseline=@($first.queues|Where-Object name -eq $queue.name)
    $historical=if($queue.name.EndsWith('.dead') -and $baseline.Count -eq 1){[long]$baseline[0].messages_ready}else{0}
    if([long]$queue.messages_ready -ne $historical -or [long]$queue.messages_unacknowledged -ne 0){throw 'Queue changed from verified baseline or main queue not drained'}
    @{name=$queue.name;baseline_ready=$historical;current_ready=$queue.messages_ready;unacked=$queue.messages_unacknowledged;delta=[long]$queue.messages_ready-$historical}
})
$audit=@{status='completed';utc=[DateTimeOffset]::UtcNow.ToString('o');historical_gate_failure_retained="$dest/state.json";baseline_utc=$first.utc;queues=$proof;reason='Historical dead-letter queue was already2 at the first P3 scrape; main queue empty and DLQ delta0. No purge or replay performed.';functional_steps=$old.steps}
$audit|ConvertTo-Json -Depth 10|Set-Content "$dest/rabbitmq-audit-recovery.json"
& "$PSScriptRoot/worker-runtime-proof.ps1" -ExpectedWorkers 1 -ExpectedSlots 2 -Output "data/perf/optimization-p3/final-recovery-runtime.json"
if($LASTEXITCODE -ne 0){throw 'Final runtime proof failed'}
Compress-Archive -Path platform,scripts/perf,compose.yaml,deploy/test/compose.optimization.yaml -DestinationPath "$dest/source-final.zip"
$audit|ConvertTo-Json -Depth 10|Set-Content "$dest/acceptance-recovery.json"
Write-Output "Functional/fault verification recovered after historical-DLQ audit: $dest"
