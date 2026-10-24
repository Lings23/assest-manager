param(
    [ValidateSet('snapshot','restore')][string]$Mode,
    [Parameter(Mandatory)][ValidatePattern('^[a-zA-Z0-9_-]+$')][string]$Name,
    [string]$EnvFile = '.env.perf',
    [string]$ComposeProject = 'asset-amqp-perf',
    [string]$OutDir = 'data/perf/20261015-local-baseline',
    [ValidateRange(1,2)][int]$Workers = 1,
    [ValidateSet('none','scoped','search')][string]$IndexProfile = 'none'
)
$ErrorActionPreference = 'Stop'
if ($ComposeProject -notin @('asset-amqp-perf','asset-amqp-opt')) { throw 'Database reset is restricted to the isolated performance projects' }
if ($IndexProfile -ne 'none' -and $ComposeProject -ne 'asset-amqp-opt') { throw 'Experimental indexes require the isolated optimization project' }
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$values=@{}
Get-Content -LiteralPath $EnvFile | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $values[$matches[1]]=$matches[2] } }
if ($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432') { throw 'Unexpected performance database configuration' }
$composeArgs=@('compose','--env-file',$EnvFile,'-p',$ComposeProject)
$databaseUser=$values.POSTGRES_USER
$databaseName=$values.POSTGRES_DB
$snapshotFile="/tmp/perf-$Name.dump"
$null=New-Item -ItemType Directory -Path $OutDir -Force
$maintenance=Join-Path $OutDir 'telemetry/maintenance.flag'
$activeSql="SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed');"
$active = & docker @composeArgs exec -T postgres psql -U $databaseUser -d $databaseName -Atc $activeSql
if ($LASTEXITCODE -ne 0 -or [int]$active.Trim() -ne 0) { throw 'Database operation requires all dispatched tasks to be terminal' }
if ($Mode -eq 'snapshot') {
    & docker @composeArgs exec -T postgres pg_dump -U $databaseUser -d $databaseName -Fc -f $snapshotFile
    if ($LASTEXITCODE -ne 0) { throw 'Database snapshot failed' }
    & docker cp "${ComposeProject}-postgres-1:$snapshotFile" (Join-Path $OutDir "$Name.dump")
    if ($LASTEXITCODE -ne 0) { throw 'Database snapshot copy failed' }
    Write-Output "Snapshot saved: $Name"
    return
}
# PostgreSQL is reset only after all application clients have stopped. MinIO
# source objects remain available; extra objects from later runs are unreferenced.
$null=New-Item -ItemType Directory -Path (Split-Path -Parent $maintenance) -Force
'Database restore in progress' | Set-Content -LiteralPath $maintenance
try {
    & docker @composeArgs stop --timeout 75 web gateway iam-service asset-service governance-service task-report-service task-publisher task-worker
    if ($LASTEXITCODE -ne 0) { throw 'Could not stop application processes before restore' }
    # Container /tmp is ephemeral after compose down; the host dump is the
    # durable checkpoint and is copied back before every restore.
    $hostDump=Join-Path $OutDir "$Name.dump"
    if(-not(Test-Path -LiteralPath $hostDump)){throw "Durable snapshot not found: $hostDump"}
    & docker cp $hostDump "${ComposeProject}-postgres-1:$snapshotFile"
    if($LASTEXITCODE -ne 0){throw 'Could not copy the durable snapshot into PostgreSQL'}
    & docker @composeArgs exec -T postgres pg_restore -U $databaseUser -d $databaseName --clean --if-exists --exit-on-error $snapshotFile
    if ($LASTEXITCODE -ne 0) { throw 'Performance database restore failed' }
    if ($IndexProfile -ne 'none') {
        & (Join-Path $PSScriptRoot 'apply-index-profile.ps1') -Profile $IndexProfile -EnvFile $EnvFile -EvidenceDir $OutDir
    }
    & docker @composeArgs up --no-build -d --wait --scale "task-worker=$Workers"
    if ($LASTEXITCODE -ne 0) { throw 'Performance application restart failed' }
    Write-Output "Restored snapshot: $Name; workers=$Workers; index_profile=$IndexProfile"
} finally {
    if (Test-Path -LiteralPath $maintenance) { Remove-Item -LiteralPath $maintenance }
}
