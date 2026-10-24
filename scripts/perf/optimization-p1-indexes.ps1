#requires -Version 7.2
# Independent experiments: unchanged projection binary, scoped indexes, then GIN.
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-sql'
$state=@{status='waiting_projection';started_utc=[DateTimeOffset]::UtcNow.ToString('o');runs=@();writes=@();profiles=@();error=$null}
function Save-State {$state|ConvertTo-Json -Depth 16|Set-Content -LiteralPath (Join-Path $out 'indexes-state.json')}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; inspect archived log"}}
function Restore-Profile([string]$Profile,[string]$Snapshot,[string]$Label){
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name $Snapshot -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile $Profile *> (Join-Path $out "$Label-restore.log")
    Assert-Exit 'Controlled snapshot/index restore'
}
function Run-Point([string]$Profile,[string]$Label,[string]$Mode,[int]$Rate,[int]$Seconds,[hashtable]$Options=@{}){
    $args=@{SeedManifest=$manifest;Mode=$Mode;Rates=@($Rate);WarmupSeconds=60;DurationSeconds=$Seconds;RunPrefix=$Label;OutDir=$out;EnvFile=$EnvFile;ComposeProject='asset-amqp-opt';MaxVUs=128;PreAllocatedVUs=128;RestoreSnapshot='d1';IndexProfile=$Profile}
    foreach($key in $Options.Keys){$args[$key]=$Options[$key]}
    & (Join-Path $PSScriptRoot 'run.ps1') @args *> (Join-Path $out "$Label.log")
    $record=@(Get-Content -LiteralPath (Join-Path $out "api/$Label-$Mode-runs.json") -Raw|ConvertFrom-Json)
    if($record.Count -ne 1){throw 'Missing unique HTTP result'}
    $state.runs+=@{profile=$Profile;label=$Label;record=$record[0]};Save-State
    if(-not $record[0].load_point_passed){throw "Unchanged HTTP thresholds failed: $Label"}
}
function Run-Search([string]$Profile){
    foreach($search in @(@{label='none';query='P20261015-100000-NOT-EXIST';min=0;max=0},@{label='rare';query='RARE1';min=20;max=20},@{label='broad';query='HALF-A';min=20;max=20})){
        $env:SEARCH_QUERY=$search.query;$env:SEARCH_MATCH=$search.label;$env:SEARCH_EXPECT_MIN="$($search.min)";$env:SEARCH_EXPECT_MAX="$($search.max)"
        try {Run-Point $Profile "p1-$Profile-search-$($search.label)" search 20 180 @{AccountUsername=$admin;AssetType='responsible-department'}}
        finally {foreach($key in @('SEARCH_QUERY','SEARCH_MATCH','SEARCH_EXPECT_MIN','SEARCH_EXPECT_MAX')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}}
    }
}
function Run-Writes([string]$Profile){
    for($round=1;$round -le 3;$round++){
        $label="write-$Profile-r$round";Restore-Profile $Profile empty $label
        $dest=Join-Path $out "writes/$Profile-r$round"
        # Same RunId yields byte-identical CSV after each independent empty restore.
        & (Join-Path $PSScriptRoot 'jobs.ps1') -EnvFile $EnvFile -OutDir $dest -RunId p1-write-fixed -Mode import -Rows 100000 -Concurrency 1 -Repetitions 1 -ComposeProject asset-amqp-opt *> (Join-Path $out "$label.log")
        $file=Join-Path $dest 'import-p1-write-fixed.manifest.json'
        $result=Get-Content -LiteralPath $file -Raw|ConvertFrom-Json
        if($result.passed -ne 1 -or $result.failed_or_unfinished -ne 0){throw "Import/SQL verification failed: $label"}
        $state.writes+=@{profile=$Profile;round=$round;manifest=$file;tasks=$result.tasks};Save-State
    }
}
function Run-Diagnostics([string]$Profile){
    Restore-Profile $Profile d1 "diagnostics-$Profile"
    $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
    $env:ASSET_PERF_DATABASE_URL=$env:ASSET_COUNT_PARITY_DATABASE_URL
    $env:ASSET_PERF_EVIDENCE_DIR=Join-Path $out "sql-$Profile"
    $env:ASSET_PERF_PHASE='p1'
    Push-Location platform
    try {
        & $toolchain test ./internal/asset -run 'TestPostgres(PerformancePlans|CountExportParity|ListProjection)' -count=1 -v -timeout 8m *> (Join-Path $out "diagnostics-$Profile.log")
        Assert-Exit 'Index plans and complete record/order/count parity'
    } finally {Pop-Location;foreach($key in @('ASSET_COUNT_PARITY_DATABASE_URL','ASSET_PERF_DATABASE_URL','ASSET_PERF_EVIDENCE_DIR','ASSET_PERF_PHASE')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}}
    & python (Join-Path $PSScriptRoot 'counts-correctness.py') --manifest $manifest --output (Join-Path $out "permissions-$Profile.json") *> (Join-Path $out "permissions-$Profile.log")
    Assert-Exit 'Index-stage seven-type permissions'
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p1-sql -EnvFile $EnvFile *> (Join-Path $out "checkpoint-$Profile.log")
    $state.profiles+=@{profile=$Profile;diagnostics_passed=$true};Save-State
}
Save-State
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(2)
    while($true){
        $projection=$null;try{$projection=Get-Content -LiteralPath (Join-Path $out 'projection-state.json') -Raw|ConvertFrom-Json}catch{}
        if($projection.status -eq 'failed'){throw "Projection failed: $($projection.error)"}
        if($projection.status -eq 'completed'){break}
        if(Test-Path (Join-Path $out 'telemetry/abort.json')){throw 'Resource stop prevents index experiment'}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'Projection wait timeout'}
        Start-Sleep -Seconds 5
    }
    $env:OPT_IMAGE_TAG='p1-projection'
    $manifest=Join-Path $repo 'data/perf/20261016-local-baseline/seed-d1/seed-manifest.json'
    $seed=Get-Content -LiteralPath $manifest -Raw|ConvertFrom-Json
    $admin=@($seed.users|Where-Object{$_.roles -contains 'admin' -and $_.data_scope -eq 'global'})[0].username
    $values=@{};Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
    $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
    Copy-Item -LiteralPath (Join-Path $repo 'data/perf/20261016-local-baseline/empty.dump') -Destination (Join-Path $out 'empty.dump')
    Compress-Archive -Path platform,scripts/perf,deploy/postgres/migrations/asset,deploy/test/compose.optimization.yaml,compose.yaml -DestinationPath (Join-Path $out 'source-p1-indexes.zip')
    $state.status='projection_controls';Save-State
    Run-Search none
    Run-Writes none
    foreach($profile in @('scoped','search')){
        $state.status="testing_$profile";Save-State
        Run-Diagnostics $profile
        Run-Point $profile "p1-$profile-list" list 80 180
        $env:LIMIT='20';$env:OFFSET='38000'
        try {Run-Point $profile "p1-$profile-deep" deep 20 180 @{AccountUsername=$admin;AssetType='responsible-department'}}finally{Remove-Item Env:LIMIT,Env:OFFSET -ErrorAction SilentlyContinue}
        Run-Search $profile
        Run-Point $profile "p1-$profile-mixed" mixed 80 600
        Run-Writes $profile
    }
    Restore-Profile search d1 final
    & python (Join-Path $PSScriptRoot 'plan-summary.py') --before-dir (Join-Path $out 'sql-projection') --after-dir (Join-Path $out 'sql-scoped') --output-stem (Join-Path $out 'scoped-plan-comparison') *> (Join-Path $out 'scoped-plan-summary.log')
    Assert-Exit 'Scoped plan comparison'
    & python (Join-Path $PSScriptRoot 'plan-summary.py') --before-dir (Join-Path $out 'sql-scoped') --after-dir (Join-Path $out 'sql-search') --output-stem (Join-Path $out 'search-plan-comparison') *> (Join-Path $out 'search-plan-summary.log')
    Assert-Exit 'Search plan comparison'
    'P1 index matrix completed'|Set-Content -LiteralPath (Join-Path $out 'telemetry/collector.stop')
    & (Join-Path $PSScriptRoot 'summarize.ps1') -ResultsDir $out -OutputJson (Join-Path $out 'observed-results.json') -OutputMarkdown (Join-Path $out 'observed-results.md') *> (Join-Path $out 'summarize.log')
    & python (Join-Path $PSScriptRoot 'resources-summary.py') --results-dir $out --output-json (Join-Path $out 'resources.json') *> (Join-Path $out 'resources-summary.log')
    Assert-Exit 'Resource summary'
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
