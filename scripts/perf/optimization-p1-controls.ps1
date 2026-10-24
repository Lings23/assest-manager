#requires -Version 7.2
# Freeze the P0 runtime while capturing P1 SQL and HTTP controls.
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-sql'
$state=@{status='waiting_p0_acceptance';started_utc=[DateTimeOffset]::UtcNow.ToString('o');runtime_image_tag='p0';sql_control_completed=$false;http_controls=@();error=$null}
function Save-State {$state|ConvertTo-Json -Depth 14|Set-Content -LiteralPath (Join-Path $out 'state.json') -Encoding utf8}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; see archived log"}}
Save-State
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(2)
    while($true){
        $p0=$null
        try{$p0=Get-Content data/perf/optimization-p0/acceptance-state.json -Raw|ConvertFrom-Json}catch{}
        if($p0.status -eq 'failed'){throw "P0 acceptance failed: $($p0.error)"}
        if($p0.status -eq 'completed'){break}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'P0 acceptance timeout'}
        Start-Sleep -Seconds 5
    }
    $env:OPT_IMAGE_TAG='p0'
    $state.status='sql_before';Save-State
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt *> (Join-Path $out 'restore-before-sql.log')
    Assert-Exit 'D1 SQL control restore'
    Compress-Archive -Path platform,scripts/perf,compose.yaml,deploy/test/compose.optimization.yaml -DestinationPath (Join-Path $out 'source-before.zip')
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p1-sql -EnvFile $EnvFile *> (Join-Path $out 'checkpoint-before.log')
    $values=@{}
    Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated optimization database'}
    $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    $env:ASSET_PERF_DATABASE_URL='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/asset_perf?sslmode=disable'
    $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out 'sql-before'
    $env:ASSET_PERF_PHASE='p1'
    Push-Location platform
    try {
        & $toolchain test ./internal/asset -run '^TestPostgresPerformancePlans$' -count=1 -v -timeout 8m *> (Join-Path $out 'sql-before.log')
        Assert-Exit 'Actual Store SQL control matrix'
    } finally {Pop-Location}
    foreach($name in @('ASSET_PERF_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
    $state.sql_control_completed=$true;$state.status='http_before';Save-State
    $collectorArguments=@('-NoProfile','-File',(Join-Path $PSScriptRoot 'collect.ps1'),'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',(Join-Path $out 'telemetry'))
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $collectorArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $out 'collector.log') -RedirectStandardError (Join-Path $out 'collector.stderr.log')
    $state.collector_pid=$collector.Id;Save-State
    $manifest=Join-Path $repo 'data/perf/20261016-local-baseline/seed-d1/seed-manifest.json'
    $seed=Get-Content -LiteralPath $manifest -Raw|ConvertFrom-Json
    $admin=@($seed.users|Where-Object{$_.roles -contains 'admin' -and $_.data_scope -eq 'global'})[0].username
    foreach($point in @(@{mode='list';rate=80;seconds=180},@{mode='deep';rate=20;seconds=180},@{mode='mixed';rate=80;seconds=600})){
        $arguments=@{SeedManifest=$manifest;Mode=$point.mode;Rates=@($point.rate);WarmupSeconds=60;DurationSeconds=$point.seconds;RunPrefix="p1-before-$($point.mode)";OutDir=$out;EnvFile=$EnvFile;ComposeProject='asset-amqp-opt';MaxVUs=128;PreAllocatedVUs=128;RestoreSnapshot='d1'}
        if($point.mode -eq 'deep'){$arguments.AccountUsername=$admin;$arguments.AssetType='responsible-department';$env:LIMIT='20';$env:OFFSET='38000'}
        & (Join-Path $PSScriptRoot 'run.ps1') @arguments *> (Join-Path $out "before-$($point.mode).log")
        foreach($name in @('LIMIT','OFFSET')){[Environment]::SetEnvironmentVariable($name,$null,'Process')}
        $records=@(Get-Content -LiteralPath (Join-Path $out "api/p1-before-$($point.mode)-$($point.mode)-runs.json") -Raw|ConvertFrom-Json)
        if($records.Count -ne 1){throw 'Missing unique HTTP control record'}
        $state.http_controls+=@{mode=$point.mode;record=$records[0]};Save-State
        if(-not $records[0].load_point_passed){$state.status='control_threshold_failed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State;return}
    }
    $state.status='controls_completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
