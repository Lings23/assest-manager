#requires -Version 7.2
# Sequential, isolated P0 verification. Never resets the historical baseline.
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p0'
$project='asset-amqp-opt'
$values=@{}
Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated optimization database'}
$compose=@('compose','--env-file',$EnvFile,'-p',$project)
$state=@{status='waiting_before_control';started_utc=[DateTimeOffset]::UtcNow.ToString('o');phase='p0';project=$project;tests=@();error=$null}
function Save-State {$state|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $out 'state.json') -Encoding utf8}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; see archived log"}}
Save-State
try {
    $deadline=[DateTimeOffset]::UtcNow.AddMinutes(15)
    while(-not(Test-Path -LiteralPath (Join-Path $out 'api/p0-before-dept-reporting-runs.json'))){
        if(Test-Path -LiteralPath (Join-Path $out 'telemetry/abort.json')){throw 'Resource stop requested'}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'Before controls did not complete'}
        Start-Sleep -Seconds 5
    }
    $state.status='functional_verification';Save-State
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject $project *> (Join-Path $out 'restore-functional.log')
    Assert-Exit 'D1 restore'
    $testDatabase='asset_opt_test_p0'
    $exists=& docker @compose exec -T postgres psql -U $values.POSTGRES_USER -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname='$testDatabase';"
    Assert-Exit 'Test database lookup'
    if([int]$exists -ne 0){throw 'Test database already exists; refusing to overwrite it'}
    & docker @compose exec -T postgres createdb -U $values.POSTGRES_USER $testDatabase
    Assert-Exit 'Test database create'
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    $env:ASSET_TEST_DATABASE_URL=$dsn+$testDatabase+'?sslmode=disable'
    $env:TASKCENTER_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:ASSET_PERF_DATABASE_URL=$env:ASSET_COUNT_PARITY_DATABASE_URL
    $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out 'sql-plans'
    $env:ASSET_PERF_PHASE='p0'
    Push-Location platform
    try {
        $goFiles=@(Get-ChildItem internal/asset/count*.go,internal/asset/performance_integration_test.go,internal/taskcenter/reporting_test.go|ForEach-Object FullName)
        & gofmt -w @goFiles
        Assert-Exit 'gofmt'
        & go test ./... -count=1 -timeout 8m *> (Join-Path $out 'functional.log')
        Assert-Exit 'Go unit and PostgreSQL integration tests'
        $state.tests+=@{name='go test ./... with isolated PostgreSQL + D1 count parity/plans';passed=$true}
        & go test ./internal/asset ./internal/taskcenter -run 'TestPostgresCountExportParity|TestPostgresCountTransportComparison' -v -count=1 *> (Join-Path $out 'count-parity-transport.log')
        Assert-Exit 'Count/export parity and transport measurement'
    } finally {Pop-Location}
    foreach($name in @('ASSET_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','ASSET_PERF_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
    $state.status='building_p0';Save-State
    & docker @compose build asset-service task-report-service *> (Join-Path $out 'build.log')
    Assert-Exit 'P0 Docker build'
    & docker @compose up --no-build -d --wait *> (Join-Path $out 'deploy.log')
    Assert-Exit 'P0 isolated deployment'
    & python (Join-Path $PSScriptRoot 'counts-correctness.py') --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output (Join-Path $out 'permissions.json') *> (Join-Path $out 'permissions.log')
    Assert-Exit 'D1 Gateway counts across all fixture permissions'
    foreach($service in @('iam-service','asset-service','governance-service','task-report-service','task-migrate','task-publisher','task-worker','gateway','web')){
        & docker tag "asset-amqp-opt-${service}:latest" "asset-amqp-opt-${service}:p0"
        Assert-Exit 'P0 image tag'
    }
    & docker ps --filter "label=com.docker.compose.project=$project" --format '{{json .}}' | Set-Content -LiteralPath (Join-Path $out 'containers-p0.jsonl')
    & docker inspect $(@(& docker ps -q --filter "label=com.docker.compose.project=$project")) --format '{"name":{{json .Name}},"image_id":{{json .Image}},"cpu_quota":{{json .HostConfig.CpuQuota}},"memory":{{json .HostConfig.Memory}}}' | Set-Content -LiteralPath (Join-Path $out 'images-resources.jsonl')
    $state.status='performance_matrix';Save-State
    foreach($scope in @('global','department')){
        $arguments=@{SeedManifest='data/perf/20261016-local-baseline/seed-d1/seed-manifest.json';Mode='reporting';Rates=@(0.1,0.2,0.5,1);WarmupSeconds=60;DurationSeconds=240;RunPrefix="p0-after-$scope";OutDir=$out;EnvFile=$EnvFile;ComposeProject=$project;AccountScope=$scope;MaxVUs=64;PreAllocatedVUs=64;RestoreSnapshot='d1'}
        if($scope -eq 'global'){$arguments.AccountRole='admin'}
        & (Join-Path $PSScriptRoot 'run.ps1') @arguments *> (Join-Path $out "after-$scope.log")
        $records=@(Get-Content -LiteralPath (Join-Path $out "api/p0-after-$scope-reporting-runs.json") -Raw|ConvertFrom-Json)
        if($records.Count -ne 4 -or @($records|Where-Object{-not $_.load_point_passed}).Count){throw "P0 $scope matrix did not pass all four loads"}
    }
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
