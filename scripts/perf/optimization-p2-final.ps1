#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p2'
$implementation=Get-Content "$out/implementation-state.json" -Raw|ConvertFrom-Json
if($implementation.status -ne 'completed'){throw 'Capacity controller must finish and restore D1 before final regression'}
$dest=Join-Path $out ('final-validation-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
$null=New-Item -ItemType Directory -Path $dest
$state=@{status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');steps=@{}}
function Save-State {$state|ConvertTo-Json -Depth 12|Set-Content "$dest/state.json"}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed; inspect preserved log"}}
Save-State
try {
    $values=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated optimization database'}
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    foreach($key in @('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL')){[Environment]::SetEnvironmentVariable($key,$dsn+'asset_opt_test_p0?sslmode=disable','Process')}
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($values.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($values.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
    $tool=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    Push-Location platform
    try {& $tool test ./... -v -count=1 -timeout 12m *> "$dest/functional.log";Check 'Final full real PG/AMQP regression'}
    finally {Pop-Location}
    $state.steps.functional=$true;Save-State
    & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$dest/permissions.json" *> "$dest/permissions.log"
    Check 'Final count permissions';$state.steps.permissions=$true;Save-State
    $env:OPT_IMAGE_TAG='p2-observe'
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> "$dest/restore.log"
    Check 'Final D1 restore';$state.steps.restore=$true;Save-State
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -EnvFile $EnvFile *> "$dest/checkpoint.log"
    Check 'Final image/config/source checkpoint'
    $sql="SELECT json_build_object('responsible_total',(SELECT count(*) FROM asset.responsible_department_assets),'responsible_active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions),'active_tasks',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished_events',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL));"
    $raw=& docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres psql -U $values.POSTGRES_USER -d asset_perf -Atc $sql
    Check 'Final D1 SQL audit'
    $audit=$raw|ConvertFrom-Json
    $audit|ConvertTo-Json|Set-Content "$dest/quiescence.json"
    if($audit.responsible_total -ne 40001 -or $audit.responsible_active -ne 38010 -or $audit.versions -ne 145002 -or $audit.active_tasks -ne 0 -or $audit.unpublished_events -ne 0){throw 'Final restored D1 or quiescence mismatch'}
    Compress-Archive -Path platform,scripts/perf,compose.yaml,deploy/test/compose.optimization.yaml -DestinationPath "$dest/source-final.zip"
    $state.steps.checkpoint=$true;$state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
    Write-Output "Final verification saved: $dest"
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {
    foreach($key in @('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL','OPT_IMAGE_TAG')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}
}
