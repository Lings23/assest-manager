#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[switch]$Resume,[ValidatePattern('^[a-zA-Z0-9_.-]{1,24}$')][string]$CapacityRunPrefix='p2-capacity')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p2'
$statePath=Join-Path $out 'implementation-state.json'
if(Test-Path $statePath){
    if(-not $Resume){throw 'Existing P2 evidence; inspect before resuming'}
    $state=Get-Content $statePath -Raw|ConvertFrom-Json -AsHashtable
}else{$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');steps=@{};error=$null}}
function Save-State {$state|ConvertTo-Json -Depth 18|Set-Content $statePath}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed; inspect preserved log"}}
function Step([string]$Name,[scriptblock]$Action){
    if($state.steps[$Name].passed){return}
    $state.status=$Name;$state.error=$null;Save-State
    & $Action
    $state.steps[$Name]=@{passed=$true;finished_utc=[DateTimeOffset]::UtcNow.ToString('o')};Save-State
}
function Restore([string]$Label){
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> "$out/$Label-restore.log"
    Check 'D1 restore'
}
function Start-Collector([bool]$Enhanced){
    $stop=Join-Path $out 'telemetry/collector.stop'
    if(Test-Path $stop){Move-Item -LiteralPath $stop -Destination "$out/telemetry/stop-prior-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))"}
    $arguments=@('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry")
    if($Enhanced){$arguments+='-Enhanced'}
    $p=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru
    $state.collector_pid=$p.Id;Save-State
}
function Stop-Collector {
    'Stage collector stopped'|Set-Content "$out/telemetry/collector.stop"
    if($state.collector_pid){Wait-Process -Id $state.collector_pid -Timeout 30 -ErrorAction SilentlyContinue}
}
function Point([string]$Label){
    & "$PSScriptRoot/run.ps1" -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -Mode mixed -Rates 80 -WarmupSeconds 60 -DurationSeconds 180 -RunPrefix $Label -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -RestoreSnapshot d1 -IndexProfile search -MaxVUs 128 -PreAllocatedVUs 128 *> "$out/$Label.log"
    $record=@(Get-Content "$out/api/$Label-mixed-runs.json" -Raw|ConvertFrom-Json)
    if($record.Count -ne 1 -or -not $record[0].load_point_passed){throw "$Label did not pass original thresholds"}
}
Save-State
try {
    $env:OPT_IMAGE_TAG='p1-batch'
    Step 'functional' {
        Restore functional
        $v=@{};Get-Content $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$v[$matches[1]]=$matches[2]}}
        $dsn='postgres://'+[Uri]::EscapeDataString($v.POSTGRES_USER)+':'+[Uri]::EscapeDataString($v.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
        foreach($key in @('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL')){[Environment]::SetEnvironmentVariable($key,$dsn+'asset_opt_test_p0?sslmode=disable','Process')}
        $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
        $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($v.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($v.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
        $tool=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
        Push-Location platform
        try {& $tool test ./... -v -count=1 -timeout 12m *> "$out/functional-serial.log";Check 'Full real PG/AMQP regression'}
        finally {Pop-Location;foreach($key in @('SERVICEKIT_TEST_DATABASE_URL','ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}}
    }
    Step 'overhead_before' {Start-Collector $false;try{Point 'p2-before-serial'}finally{Stop-Collector}}
    Step 'build' {
        Compress-Archive -Path platform,scripts/perf,compose.yaml,deploy/test/compose.optimization.yaml -DestinationPath "$out/source-p2-observe.zip"
        & "$PSScriptRoot/build-optimization.ps1" -StageTag p2-observe -OutDir $out -Services asset-service,iam-service,gateway,governance-service,task-report-service,task-worker,task-publisher *> "$out/build.log"
        Check 'P2 image build'
    }
    $env:OPT_IMAGE_TAG='p2-observe'
    Step 'metrics_verification' {
        Restore verification
        Start-Collector $true
        try {
            $verification=Join-Path $out ('verification-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
            $null=New-Item -ItemType Directory -Path $verification
            $state.verification_dir=$verification;Save-State
            & python "$PSScriptRoot/counts-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output "$out/permissions.json" *> "$out/permissions.log";Check 'Reporting permissions'
            & python "$PSScriptRoot/asset-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir "$verification/crud" *> "$out/crud.log";Check 'Seven-type real CRUD'
            & python "$PSScriptRoot/wide-import.py" --env-file $EnvFile --out-dir "$verification/import" --run-id p2-observer-import --case wide --rows 500 *> "$out/import.log";Check 'Worker instrumented import'
            & python "$PSScriptRoot/export-correctness.py" --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir "$verification/exports" *> "$out/exports.log";Check 'Worker instrumented exports'
            & python "$PSScriptRoot/metrics-verify.py" --out-dir $verification *> "$out/metrics-verification.log";Check 'Actual metric names, private access and stages'
        } finally {Stop-Collector}
    }
    Step 'overhead_after' {Start-Collector $true;try{Point 'p2-after-serial'}finally{Stop-Collector}}
    @{functional_passed=$true;metrics_verified=$true;overhead_pair_completed=$true;image_tag='p2-observe';baseline_run='p2-before-serial';observer_run='p2-after-serial';note='Pair includes new instrumentation plus enhanced collector; no pool/resource/threshold change.'}|ConvertTo-Json|Set-Content "$out/implementation-ready.json"
    $state.status='implementation_ready';Save-State
    Step 'capacity' {
        Start-Collector $true
        try {
            $resumeCapacity=Test-Path "$out/$CapacityRunPrefix-state.json"
            & "$PSScriptRoot/optimization-capacity.ps1" -EnvFile $EnvFile -RunPrefix $CapacityRunPrefix -Resume:$resumeCapacity *> "$out/$CapacityRunPrefix-driver.log"
            Check 'Capacity experiment controller'
        }finally{Stop-Collector}
    }
    Restore final
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -EnvFile $EnvFile *> "$out/checkpoint-final.log"
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
}catch{$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
