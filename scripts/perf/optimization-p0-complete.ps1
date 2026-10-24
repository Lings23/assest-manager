#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p0'
$final=@{status='waiting_matrix';started_utc=[DateTimeOffset]::UtcNow.ToString('o');error=$null}
function Save-Final {$final|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $out 'acceptance-state.json') -Encoding utf8}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; inspect archived evidence"}}
Save-Final
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(2)
    while($true){
        if(Test-Path -LiteralPath (Join-Path $out 'telemetry/abort.json')){throw 'Resource stop prevents P0 completion'}
        $matrix=$null
        try{$matrix=Get-Content -LiteralPath (Join-Path $out 'state.json') -Raw|ConvertFrom-Json}catch{}
        if($matrix.status -eq 'failed'){throw "P0 matrix failed: $($matrix.error)"}
        if($matrix.status -eq 'completed'){break}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'P0 matrix completion timeout'}
        Start-Sleep -Seconds 5
    }
    'Formal P0 matrix completed'|Set-Content -LiteralPath (Join-Path $out 'telemetry/collector.stop')
    Start-Sleep -Seconds 12
    $final.status='canonical_regression';Save-Final
    $values=@{}
    Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database'}
    $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    if(-not(Test-Path -LiteralPath $toolchain)){throw 'Cached matching Go1.25.13 toolchain is unavailable'}
    & $toolchain version|Set-Content -LiteralPath (Join-Path $out 'canonical-test-toolchain.txt')
    Assert-Exit 'Matching Go toolchain'
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    $env:ASSET_TEST_DATABASE_URL=$dsn+'asset_opt_test_p0?sslmode=disable'
    $env:TASK_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
    $env:TASKCENTER_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($values.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($values.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
    Push-Location platform
    try {
        & $toolchain test ./... -v -count=1 -timeout 12m *> (Join-Path $out 'canonical-functional.log')
        Assert-Exit 'Matching-toolchain PostgreSQL and RabbitMQ regression'
        $final.functional_regression_passed=$true;Save-Final
        $env:ASSET_PERF_DATABASE_URL=$env:ASSET_COUNT_PARITY_DATABASE_URL
        $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out 'sql-plans-canonical'
        $env:ASSET_PERF_PHASE='p0'
        & $toolchain test ./internal/asset -run '^TestPostgresPerformancePlans$' -v -count=1 -timeout 5m *> (Join-Path $out 'sql-plans-canonical.log')
        Assert-Exit 'Sequential Count/Export SQL plans'
        $final.sql_plans_passed=$true;Save-Final
    } finally {Pop-Location}
    foreach($name in @('ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL','ASSET_PERF_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
    & python (Join-Path $PSScriptRoot 'counts-correctness.py') --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output (Join-Path $out 'permissions-after.json') *> (Join-Path $out 'permissions-after.log')
    Assert-Exit 'Post-matrix D1 permissions'
    & (Join-Path $PSScriptRoot 'summarize.ps1') -ResultsDir $out -OutputJson (Join-Path $out 'observed-results.json') -OutputMarkdown (Join-Path $out 'observed-results.md') *> (Join-Path $out 'summarize.log')
    & python (Join-Path $PSScriptRoot 'resources-summary.py') --results-dir $out --output-json (Join-Path $out 'resources.json') *> (Join-Path $out 'resource-summary.log')
    Assert-Exit 'Resource summary'
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p0 -EnvFile $EnvFile *> (Join-Path $out 'checkpoint-final.log')
    $final.status='completed';$final.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-Final
} catch {$final.status='failed';$final.error=$_.Exception.Message;Save-Final;throw}
