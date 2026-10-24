#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[switch]$Resume)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-import'
$sqlOut=Join-Path $repo 'data/perf/optimization-p1-sql'
$statePath=Join-Path $out 'state.json'
if(Test-Path -LiteralPath $statePath){
    if(-not $Resume){throw 'Existing import orchestration evidence; use Resume after inspecting it'}
    $state=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json -AsHashtable
}else{$state=@{status='waiting_indexes';started_utc=[DateTimeOffset]::UtcNow.ToString('o');steps=@{};cases=@();writes=@();error=$null}}
function Save-State {$state|ConvertTo-Json -Depth 20|Set-Content -LiteralPath $statePath}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; inspect archived log"}}
function Assert-Resources {
    $os=Get-CimInstance Win32_OperatingSystem
    if([double]$os.FreePhysicalMemory/[double]$os.TotalVisibleMemorySize -lt 0.15){throw 'Host free memory below the original 15% guard'}
    if(Test-Path (Join-Path $out 'telemetry/abort.json')){throw 'Resource collector stop condition prevents another import'}
}
function Step([string]$Name,[scriptblock]$Action){
    if($state.steps[$Name].passed){return}
    $state.status=$Name;$state.error=$null;Save-State
    & $Action
    $state.steps[$Name]=@{passed=$true;finished_utc=[DateTimeOffset]::UtcNow.ToString('o')};Save-State
}
function Restore([string]$Snapshot,[string]$Label){
    Assert-Resources
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name $Snapshot -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> (Join-Path $out "$Label-restore.log")
    Assert-Exit 'D1/empty restore with unchanged accepted indexes'
}
function Wide-Case([string]$Name,[string]$Case,[int]$Rows,[string]$Expected='succeeded'){
    Restore d1 $Name
    $dest=Join-Path $out ('cases/'+$Name+'-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
    & python (Join-Path $PSScriptRoot 'wide-import.py') --env-file $EnvFile --out-dir $dest --run-id $Name --case $Case --rows $Rows --expect $Expected *> (Join-Path $out "$Name.log")
    Assert-Exit "Real Gateway/SQL case $Name"
    $result=Get-Content -LiteralPath (Join-Path $dest 'result.json') -Raw|ConvertFrom-Json
    if(-not $result.passed){throw "Import case failed: $Name"}
    $state.cases+=@{name=$Name;path=$dest;result=$result};Save-State
}
Save-State
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(5)
    while($true){
        $matrix=$null;try{$matrix=Get-Content -LiteralPath (Join-Path $sqlOut 'indexes-state.json') -Raw|ConvertFrom-Json}catch{}
        if($matrix.status -eq 'failed'){throw "P1-A failed: $($matrix.error)"}
        if($matrix.status -eq 'completed'){break}
        if(Test-Path (Join-Path $sqlOut 'telemetry/abort.json')){throw 'P1-A resource stop prevents P1-B'}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'P1-A wait timeout'}
        Start-Sleep -Seconds 5
    }
    Step 'p1_sql_final_audit' {
        & (Join-Path $PSScriptRoot 'optimization-p1-final-audit.ps1') -EnvFile $EnvFile *> (Join-Path $out 'p1-sql-final-audit.log')
        if((Get-Content -LiteralPath (Join-Path $sqlOut 'acceptance-state.json') -Raw|ConvertFrom-Json).status -ne 'completed'){throw 'P1-A final migration/SQL audit failed'}
    }
    foreach($snapshot in @('d1','empty')){if(-not(Test-Path (Join-Path $out "$snapshot.dump"))){Copy-Item -LiteralPath (Join-Path $sqlOut "$snapshot.dump") -Destination (Join-Path $out "$snapshot.dump")}}
    if(Test-Path (Join-Path $out 'telemetry/collector.stop')){Move-Item -LiteralPath (Join-Path $out 'telemetry/collector.stop') -Destination (Join-Path $out ('telemetry/collector-stop-prior-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff')))}
    $collectorArgs=@('-NoProfile','-File',(Join-Path $PSScriptRoot 'collect.ps1'),'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',(Join-Path $out 'telemetry'))
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $collectorArgs -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $out 'collector.log') -RedirectStandardError (Join-Path $out 'collector.stderr.log')
    $state.collector_pid=$collector.Id;Save-State
    $env:OPT_IMAGE_TAG='p1-projection'
    Step 'before_wide_failure' {Wide-Case 'p1b-before-wide500' wide 500 dead_letter}
    Step 'functional' {
        Restore d1 functional
        $values=@{};Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
        $dsn='postgres://'+[Uri]::EscapeDataString($values.POSTGRES_USER)+':'+[Uri]::EscapeDataString($values.POSTGRES_PASSWORD)+'@127.0.0.1:59432/'
        $toolchain=Join-Path (& go env GOMODCACHE) 'golang.org/toolchain@v0.0.1-go1.25.13.windows-amd64/bin/go.exe'
        $env:ASSET_TEST_DATABASE_URL=$dsn+'asset_opt_test_p0?sslmode=disable';$env:TASK_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL;$env:TASKCENTER_TEST_DATABASE_URL=$env:ASSET_TEST_DATABASE_URL
        $env:ASSET_COUNT_PARITY_DATABASE_URL=$dsn+'asset_perf?sslmode=disable'
        $env:TASKCENTER_AMQP_TEST_URL='amqp://'+[Uri]::EscapeDataString($values.RABBITMQ_USER)+':'+[Uri]::EscapeDataString($values.RABBITMQ_PASSWORD)+'@127.0.0.1:59672/'
        Push-Location platform
        try {& $toolchain test ./... -v -count=1 -timeout 12m *> (Join-Path $out 'functional.log');Assert-Exit 'Byte-splitting and full real PG/AMQP regression'}
        finally {Pop-Location;foreach($key in @('ASSET_TEST_DATABASE_URL','TASK_TEST_DATABASE_URL','TASKCENTER_TEST_DATABASE_URL','ASSET_COUNT_PARITY_DATABASE_URL','TASKCENTER_AMQP_TEST_URL')){[Environment]::SetEnvironmentVariable($key,$null,'Process')}}
    }
    Step 'build' {
        Compress-Archive -Path platform,scripts/perf,deploy/postgres/migrations/asset,deploy/test/compose.optimization.yaml,compose.yaml -DestinationPath (Join-Path $out ('source-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff')+'.zip'))
        & (Join-Path $PSScriptRoot 'build-optimization.ps1') -StageTag p1-batch -OutDir $out -Services asset-service,task-worker *> (Join-Path $out 'build.log')
    }
    $env:OPT_IMAGE_TAG='p1-batch'
    foreach($case in @(@{name='ordinary500';kind='ordinary';rows=500},@{name='wide500';kind='wide';rows=500},@{name='wide1000';kind='wide';rows=1000},@{name='wide10000';kind='wide';rows=10000},@{name='wide100000';kind='wide';rows=100000},@{name='near';kind='near';rows=1},@{name='oversize';kind='oversize';rows=1})){
        $expected=if($case.kind -eq 'oversize'){'dead_letter'}else{'succeeded'}
        Step ('after_'+$case.name) {Wide-Case ('p1b-after-'+$case.name) $case.kind $case.rows $expected}
    }
    for($round=1;$round -le 3;$round++){
        Step "normal_f1_$round" {
            Restore empty "f1-$round"
            $dest=Join-Path $out "normal-f1/r$round"
            & (Join-Path $PSScriptRoot 'jobs.ps1') -EnvFile $EnvFile -OutDir $dest -RunId p1-write-fixed -Mode import -Rows 100000 -Concurrency 1 -Repetitions 1 -ComposeProject asset-amqp-opt *> (Join-Path $out "normal-f1-r$round.log")
            $result=Get-Content -LiteralPath (Join-Path $dest 'import-p1-write-fixed.manifest.json') -Raw|ConvertFrom-Json
            if($result.passed -ne 1 -or $result.failed_or_unfinished -ne 0){throw 'Normal F1/SQL regression failed'}
            $state.writes+=@{round=$round;tasks=$result.tasks};Save-State
        }
    }
    Step 'final_d1' {
        Restore d1 final
        & python (Join-Path $PSScriptRoot 'counts-correctness.py') --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --output (Join-Path $out 'permissions-final.json') *> (Join-Path $out 'permissions-final.log')
        Assert-Exit 'Post-import permission counts'
        & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p1-import -EnvFile $EnvFile *> (Join-Path $out 'checkpoint-final.log')
    }
    'P1-B completed'|Set-Content -LiteralPath (Join-Path $out 'telemetry/collector.stop')
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {
    $state.status='failed';$state.error=$_.Exception.Message;Save-State
    if(Test-Path (Join-Path $out 'telemetry/abort.json')){& docker compose --env-file $EnvFile -p asset-amqp-opt stop --timeout 10 task-worker *> (Join-Path $out 'resource-stop-worker.log')}
    throw
} finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
