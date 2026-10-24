#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-sql'
$state=@{status='waiting_controls';started_utc=[DateTimeOffset]::UtcNow.ToString('o');phase='projection_only';indexes_changed=$false;tests=@();runs=@();error=$null}
function Save-State {$state|ConvertTo-Json -Depth 14|Set-Content -LiteralPath (Join-Path $out 'projection-state.json') -Encoding utf8}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; inspect archived evidence"}}
Save-State
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(2)
    while($true){
        $control=$null
        try{$control=Get-Content -LiteralPath (Join-Path $out 'state.json') -Raw|ConvertFrom-Json}catch{}
        if($control.status -eq 'failed'){throw "Control orchestration failed: $($control.error)"}
        if($control.status -in @('controls_completed','control_threshold_failed')){break}
        if(Test-Path (Join-Path $out 'telemetry/abort.json')){throw 'Resource stop prevents projection experiment'}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'P1 control timeout'}
        Start-Sleep -Seconds 5
    }
    $env:OPT_IMAGE_TAG='p0'
    $state.status='functional_verification';Save-State
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt *> (Join-Path $out 'projection-restore-functional.log')
    Assert-Exit 'Projection D1 restore'
    $values=@{}
    Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database'}
    $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    $env:ASSET_TEST_DATABASE_URL=$dsn+'asset_opt_test_p0?sslmode=disable'
    $env:TASK_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
    $env:TASKCENTER_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($values.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($values.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
    Push-Location platform
    try {
        & $toolchain test ./... -v -count=1 -timeout 12m *> (Join-Path $out 'projection-functional.log')
        Assert-Exit 'Projection semantics and real dependency regression'
        $state.tests+=@{name='Go1.25.13 platform + PG/AMQP + complete record/order parity';passed=$true};Save-State
        $env:ASSET_PERF_DATABASE_URL=$env:ASSET_COUNT_PARITY_DATABASE_URL
        $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out 'sql-projection'
        $env:ASSET_PERF_PHASE='p1'
        & $toolchain test ./internal/asset -run '^TestPostgresPerformancePlans$' -v -count=1 -timeout 8m *> (Join-Path $out 'sql-projection.log')
        Assert-Exit 'Projection SQL matrix'
    } finally {Pop-Location}
    foreach($name in @('ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL','ASSET_PERF_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
    $state.status='building';Save-State
    & (Join-Path $PSScriptRoot 'build-optimization.ps1') -StageTag p1-projection -OutDir $out -Services asset-service *> (Join-Path $out 'projection-build.log')
    $env:OPT_IMAGE_TAG='p1-projection'
    $maintenance=Join-Path $out 'telemetry/maintenance.flag'
    'Controlled projection deployment without active load'|Set-Content -LiteralPath $maintenance
    try {
        & docker compose --env-file $EnvFile -p asset-amqp-opt up --no-build -d --wait *> (Join-Path $out 'projection-deploy.log')
        Assert-Exit 'Projection deployment'
    } finally {Remove-Item -LiteralPath $maintenance -ErrorAction SilentlyContinue}
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p1-sql -EnvFile $EnvFile *> (Join-Path $out 'projection-checkpoint.log')
    $state.status='http_matrix';Save-State
    $manifest=Join-Path $repo 'data/perf/20261016-local-baseline/seed-d1/seed-manifest.json'
    $seed=Get-Content -LiteralPath $manifest -Raw|ConvertFrom-Json
    $admin=@($seed.users|Where-Object{$_.roles -contains 'admin' -and $_.data_scope -eq 'global'})[0].username
    foreach($point in @(@{mode='list';rate=80;seconds=180},@{mode='deep';rate=20;seconds=180},@{mode='mixed';rate=80;seconds=600})){
        $arguments=@{SeedManifest=$manifest;Mode=$point.mode;Rates=@($point.rate);WarmupSeconds=60;DurationSeconds=$point.seconds;RunPrefix="p1-projection-$($point.mode)";OutDir=$out;EnvFile=$EnvFile;ComposeProject='asset-amqp-opt';MaxVUs=128;PreAllocatedVUs=128;RestoreSnapshot='d1'}
        if($point.mode -eq 'deep'){$arguments.AccountUsername=$admin;$arguments.AssetType='responsible-department';$env:LIMIT='20';$env:OFFSET='38000'}
        & (Join-Path $PSScriptRoot 'run.ps1') @arguments *> (Join-Path $out "projection-$($point.mode).log")
        foreach($name in @('LIMIT','OFFSET')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
        $records=@(Get-Content -LiteralPath (Join-Path $out "api/p1-projection-$($point.mode)-$($point.mode)-runs.json") -Raw|ConvertFrom-Json)
        if($records.Count -ne 1){throw 'Projection HTTP record missing or ambiguous'}
        $state.runs+=@{mode=$point.mode;record=$records[0]};Save-State
        if(-not $records[0].load_point_passed){throw "Projection $($point.mode) regression did not meet unchanged thresholds"}
    }
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
