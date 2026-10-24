#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-sql'
$state=@{status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');profiles=@();error=$null}
function Save-State {$state|ConvertTo-Json -Depth 10|Set-Content -LiteralPath (Join-Path $out 'acceptance-state.json')}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed"}}
Save-State
try {
    if((Get-Content -LiteralPath (Join-Path $out 'indexes-state.json') -Raw|ConvertFrom-Json).status -ne 'completed'){throw 'Index matrix is not complete'}
    & (Join-Path $PSScriptRoot 'index-migration-check.ps1') -EnvFile $EnvFile *> (Join-Path $out 'migration-compatibility.log')
    $state.migrations_passed=$true;Save-State
    $env:OPT_IMAGE_TAG='p1-projection'
    $values=@{};Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/asset_perf?sslmode=disable'
    $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    foreach($profile in @('none','scoped','search')){
        & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile $profile *> (Join-Path $out "analyzed-$profile-restore.log")
        Assert-Exit 'Matched D1 restore'
        # pg_dump does not preserve planner statistics. Refresh every profile
        # explicitly for this separate SQL-only causal comparison.
        & docker compose --env-file $EnvFile -p asset-amqp-opt exec -T postgres vacuumdb -U $values.POSTGRES_USER -d asset_perf --analyze-only --schema=asset *> (Join-Path $out "analyzed-$profile-statistics.log")
        Assert-Exit 'Matched planner statistics'
        $env:ASSET_PERF_DATABASE_URL=$dsn;$env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn
        $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out "sql-analyzed-$profile";$env:ASSET_PERF_PHASE='p1'
        Push-Location platform
        try {
            & $toolchain test ./internal/asset -run 'TestPostgres(PerformancePlans|CountExportParity|ListProjection)' -v -count=1 -timeout 8m *> (Join-Path $out "analyzed-$profile.log")
            Assert-Exit 'Matched SQL and complete-record parity'
        } finally {Pop-Location}
        $state.profiles+=@{profile=$profile;plans_and_parity_passed=$true};Save-State
    }
    foreach($pair in @(@{before='none';after='scoped'},@{before='scoped';after='search'})){
        & python (Join-Path $PSScriptRoot 'plan-summary.py') --before-dir (Join-Path $out "sql-analyzed-$($pair.before)") --after-dir (Join-Path $out "sql-analyzed-$($pair.after)") --output-stem (Join-Path $out "analyzed-$($pair.after)-comparison") *> (Join-Path $out "analyzed-$($pair.after)-comparison.log")
        Assert-Exit 'Matched analyzed plan comparison'
    }
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {foreach($key in @('ASSET_PERF_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE','OPT_IMAGE_TAG')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}}
