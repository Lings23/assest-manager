#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[string]$OutDir='data/perf/optimization-p2/preauthenticated-20261018')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=(Resolve-Path -LiteralPath $OutDir).Path
if(-not $out.StartsWith((Join-Path $repo 'data/perf/optimization-p2/preauthenticated-'),[StringComparison]::OrdinalIgnoreCase)){throw 'Unexpected isolated protocol archive'}
$controller=Get-Content "$out/state.json" -Raw|ConvertFrom-Json
if($controller.status -ne 'completed'){throw 'Wait for all pressure and initial D1 restore to finish'}
if(Test-Path "$out/verification-state.json"){throw 'Preserve existing final verification; inspect before any rerun'}
$verification=@{status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');steps=@{}}
function Save-State {$verification|ConvertTo-Json -Depth 8|Set-Content "$out/verification-state.json"}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed; inspect preserved evidence"}}
Save-State
try {
    if(Test-Path "$out/resource-policy-handoff.json"){
        $handoff=Get-Content "$out/resource-policy-handoff.json" -Raw|ConvertFrom-Json
        'Pressure complete; stop the policy collector'|Set-Content "$out/telemetry/collector.stop"
        Wait-Process -Id $handoff.new_collector_pid -Timeout 30 -ErrorAction SilentlyContinue
    }
    & python "$PSScriptRoot/preauth-acceptance.py" --results-dir $out *> "$out/acceptance.log";Check 'Offline capacity and authentication certificate'
    $verification.steps.capacity=$true;Save-State
    & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$out/permissions-final.json" *> "$out/permissions-final.log";Check 'Final147 count permissions'
    $verification.steps.permissions=$true;Save-State
    $env:OPT_IMAGE_TAG='p2-observe'
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> "$out/verification-restore.log";Check 'Final D1 restore after permission bookkeeping'
    $values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database'}
    $sql="SELECT json_build_object('responsible_total',(SELECT count(*) FROM asset.responsible_department_assets),'responsible_active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions),'active_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $sql
    Check 'Final D1 SQL audit';$audit=$raw|ConvertFrom-Json
    $audit|ConvertTo-Json|Set-Content "$out/quiescence-final.json"
    if($audit.responsible_total -ne 40001 -or $audit.responsible_active -ne 38010 -or $audit.versions -ne 145002 -or $audit.active_tasks -ne 0 -or $audit.unpublished_events -ne 0){throw 'Restored D1/quiescence mismatch'}
    $expected=Get-Content data/perf/optimization-p2/checkpoint-20261018T034834327/containers.jsonl|ForEach-Object{$_|ConvertFrom-Json}
    foreach($item in $expected|Where-Object{$_.name -match 'asset-amqp-opt-(gateway|iam-service|asset-service|governance-service|task-report-service|task-worker|task-publisher)-1$'}){
        $actual=& docker inspect $item.name.TrimStart('/') --format '{{.Image}}';Check 'Final image identity'
        if($actual.Trim() -ne $item.image_id){throw 'Application image changed'}
    }
    $verification.steps.restored_d1=$true;$verification.steps.application_images_unchanged=$true;Save-State
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -EnvFile $EnvFile *> "$out/checkpoint-verified.log";Check 'Final checkpoint'
    $verification.status='completed';$verification.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
}catch{$verification.status='failed';$verification.error=$_.Exception.Message;Save-State;throw}
finally{Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
