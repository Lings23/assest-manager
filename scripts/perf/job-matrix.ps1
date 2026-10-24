#requires -Version 7.2
<#
S5/S6/F2 and worker scaling, with a restore before every measured batch.
PlanOnly writes the complete plan without connecting to Docker or any service.
Execution requires exclusive ownership of the isolated asset-amqp-perf project;
database.ps1 stops its application containers while restoring each checkpoint.
MinIO is retained so all checkpoint object references remain usable.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$OutDir,
    [Parameter(Mandatory)][string]$SnapshotRoot,
    [string]$EnvFile='.env.perf',
    [string]$BaseUrl='http://127.0.0.1:59080/api/v1',
    [ValidateSet('asset-amqp-perf')][string]$ComposeProject='asset-amqp-perf',
    [ValidatePattern('^[a-zA-Z0-9_-]{1,30}$')][string]$BaseSnapshot='empty',
    [ValidatePattern('^[a-zA-Z0-9_-]{1,24}$')][string]$RunPrefix='files-20261016',
    [ValidateSet('All','Prepare','F1','F2','S6','Scaling')][string]$Stage='All',
    [ValidateRange(1,10)][int]$Repetitions=3,
    [ValidateRange(10,1800)][int]$WaitSeconds=1200,
    [switch]$FullScalingComparison,
    [switch]$PlanOnly
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$OutDir=[IO.Path]::GetFullPath($OutDir)
$SnapshotRoot=[IO.Path]::GetFullPath($SnapshotRoot)
$perfRoot=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
foreach($path in @($OutDir,$SnapshotRoot)) {
    if(-not $path.StartsWith($perfRoot,[StringComparison]::OrdinalIgnoreCase)) {throw 'OutDir and SnapshotRoot must be inside ignored data/perf'}
}
[void][IO.Directory]::CreateDirectory($OutDir)
$suiteDir=Join-Path $OutDir ('jobs/matrix-'+$RunPrefix)
[void][IO.Directory]::CreateDirectory($suiteDir)
$planPath=Join-Path $suiteDir 'plan.json'
$manifestPath=Join-Path $suiteDir ('execution-'+$Stage+'.json')
$eventsPath=Join-Path $suiteDir 'execution.jsonl'
$checkpointPath=Join-Path $suiteDir 'export-checkpoints.json'
$abortPath=Join-Path $OutDir 'telemetry/abort.json'
$jobsPath=Join-Path $PSScriptRoot 'jobs.ps1'
$databasePath=Join-Path $PSScriptRoot 'database.ps1'
$points=[Collections.Generic.List[object]]::new()
foreach($count in @(1000,10000,100000)) {
    for($round=1;$round -le $Repetitions;$round++) {
        $points.Add([ordered]@{id="f1-n$count-c1-r$round-w1";section='F1';profile='F1';kind='import';rows=$count;concurrency=1;round=$round;workers=1;snapshot=$BaseSnapshot;run_id="$RunPrefix-i$count-c1-r$round"})
    }
}
foreach($parallel in @(2,4)) {
    for($round=1;$round -le $Repetitions;$round++) {
        $points.Add([ordered]@{id="f1-n100000-c$parallel-r$round-w1";section='F1';profile='F1';kind='import';rows=100000;concurrency=$parallel;round=$round;workers=1;snapshot=$BaseSnapshot;run_id="$RunPrefix-i100000-c$parallel-r$round"})
    }
}
foreach($count in @(10000,100000)) {
    for($round=1;$round -le $Repetitions;$round++) {
        $points.Add([ordered]@{id="f2-n$count-c1-r$round-w1";section='F2';profile='F2';kind='roundtrip';rows=$count;concurrency=1;round=$round;workers=1;snapshot=$BaseSnapshot;run_id="$RunPrefix-f2-$count-r$round"})
    }
}
foreach($count in @(10000,100000)) {
    foreach($parallel in @(1,2,4)) {
        for($round=1;$round -le $Repetitions;$round++) {
            $points.Add([ordered]@{id="s6-n$count-c$parallel-r$round-w1";section='S6';profile='F1';kind='export';rows=$count;concurrency=$parallel;round=$round;workers=1;snapshot="$RunPrefix-exp$count";run_id="$RunPrefix-e$count-c$parallel-r$round"})
        }
    }
}
$baselinePoints=@($points | Where-Object {$_.section -in @('F1','S6')})
foreach($baseline in $baselinePoints) {
    if(-not $FullScalingComparison -and ($baseline.rows -ne 100000 -or $baseline.concurrency -ne 4)) {continue}
    $copy=[ordered]@{}
    foreach($key in $baseline.Keys) {$copy[$key]=$baseline[$key]}
    $copy.id=$copy.id.Replace('-w1','-w2');$copy.section='Scaling';$copy.workers=2
    $copy.comparison_point=$baseline.id
    $points.Add($copy)
}
$taskCount=0
foreach($point in $points) {$taskCount+=$point.concurrency*$(if($point.kind -eq 'roundtrip'){2}else{1})}
$selected=@($points | Where-Object {$Stage -eq 'All' -or $_.section -eq $Stage})
$plan=[ordered]@{
    run_prefix=$RunPrefix;stage=$Stage;repetitions=$Repetitions;full_scaling_comparison=[bool]$FullScalingComparison
    measured_batches=$points.Count;measured_new_tasks=$taskCount;export_preparation_new_tasks=4;all_stage_new_tasks=$taskCount+4
    selected_batches=$selected.Count;selected_measured_new_tasks=0
    snapshot_root=$SnapshotRoot;base_snapshot=$BaseSnapshot;out_dir=$OutDir
    script_sha256=@{jobs=(Get-FileHash -LiteralPath $jobsPath -Algorithm SHA256).Hash;database=(Get-FileHash -LiteralPath $databasePath -Algorithm SHA256).Hash;matrix=(Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash;verification=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'verify.sql') -Algorithm SHA256).Hash}
    workers_slots_per_replica=2
    concurrency_scope='Concurrent full client lifecycles; upload preparation and preflight can overlap. Actual simultaneous worker execution must be obtained from telemetry.'
    comparisons='Each repetition restores an identical database checkpoint. Worker pairs reuse the same RunId and source CSV bytes in separate output directories.'
    frozen_export='An empty responsible-department baseline is populated through Gateway, exported and verified against its import CSV; the full exported CSV becomes the frozen expected content.'
    f2='Each single import is followed by a separately timed export against the original wide CSV, including quoted multiline remarks.'
    estimate='About 1.5-3 hours for the default full suite when tasks finish in seconds and each restore takes 30-90 seconds; large-task execution and validation can extend this. No duration guarantee.'
    points=$points.ToArray()
}
foreach($point in $selected) {$plan.selected_measured_new_tasks+=$point.concurrency*$(if($point.kind -eq 'roundtrip'){2}else{1})}
[IO.File]::WriteAllText($planPath,($plan|ConvertTo-Json -Depth 16),[Text.UTF8Encoding]::new($false))
if($PlanOnly) {
    Write-Output "Plan only: batches=$($plan.measured_batches) tasks=$($plan.all_stage_new_tasks) selected_batches=$($plan.selected_batches) plan=$planPath"
    return
}
$EnvFile=(Resolve-Path -LiteralPath $EnvFile).Path
$envValues=@{}
foreach($line in [IO.File]::ReadAllLines($EnvFile)) {
    if($line -match '^\s*([^#=]+)=(.*)$') {$envValues[$matches[1].Trim()]=$matches[2].Trim().Trim('"').Trim("'")}
}
if($envValues.POSTGRES_DB -ne 'asset_perf' -or $envValues.POSTGRES_PORT -ne '59432' -or $envValues.GATEWAY_PORT -ne '59080') {throw 'Unexpected isolated performance environment ports/database'}
if($envValues.WORKER_SLOTS -and [int]$envValues.WORKER_SLOTS -ne 2) {throw 'S8 requires WORKER_SLOTS=2 for both worker counts'}
$gateway=[Uri]$BaseUrl
if($gateway.Host -notin @('127.0.0.1','localhost','::1') -or $gateway.Port -ne 59080 -or $gateway.AbsolutePath.TrimEnd('/') -ne '/api/v1') {throw 'BaseUrl must address the isolated local Gateway on port 59080'}
if(-not(Test-Path -LiteralPath (Join-Path $SnapshotRoot ($BaseSnapshot+'.dump')))) {throw 'The durable empty baseline dump is missing from SnapshotRoot'}
$composeArgs=@('compose','-f',(Join-Path $repoRoot 'compose.yaml'),'--env-file',$EnvFile,'-p',$ComposeProject)
$dbUser=$envValues.POSTGRES_USER;$dbName=$envValues.POSTGRES_DB
$rabbitPort=if($envValues.RABBITMQ_MANAGEMENT_PORT){[int]$envValues.RABBITMQ_MANAGEMENT_PORT}else{59673}
$rabbitUser=if($envValues.RABBITMQ_USER){$envValues.RABBITMQ_USER}else{'asset'}
$rabbitHeader=@{Authorization='Basic '+[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($rabbitUser+':'+$envValues.RABBITMQ_PASSWORD))}
$records=[Collections.Generic.List[object]]::new()
$state=[ordered]@{started_utc=[DateTimeOffset]::UtcNow.ToString('o');stage=$Stage;status='running';planned_measured_batches=$selected.Count;planned_measured_tasks=$plan.selected_measured_new_tasks;completed_batches=0;current=$null;error=$null;records=@()}
$checkpoints=@{}
if(Test-Path -LiteralPath $checkpointPath) {
    $old=Get-Content -Raw -LiteralPath $checkpointPath|ConvertFrom-Json -AsHashtable
    foreach($key in $old.Keys) {$checkpoints[$key]=$old[$key]}
}
function Save-Matrix {
    $state.records=$records.ToArray()
    $state.updated_utc=[DateTimeOffset]::UtcNow.ToString('o')
    [IO.File]::WriteAllText($manifestPath,($state|ConvertTo-Json -Depth 30),[Text.UTF8Encoding]::new($false))
}
function Write-Event([string]$Kind,$Details) {
    $event=@{utc=[DateTimeOffset]::UtcNow.ToString('o');kind=$Kind;details=$Details}
    [IO.File]::AppendAllText($eventsPath,(($event|ConvertTo-Json -Compress -Depth 15)+"`n"))
}
function Assert-Resources {
    if(Test-Path -LiteralPath $abortPath) {throw 'Collector stop condition exists; inspect telemetry/abort.json before submitting another task'}
    $os=Get-CimInstance Win32_OperatingSystem
    $ratio=[double]$os.FreePhysicalMemory/[double]$os.TotalVisibleMemorySize
    if($ratio -lt 0.15) {throw ('Host free memory {0:P1} is below 15%; no new task is permitted' -f $ratio)}
    $drive=[IO.Path]::GetPathRoot($OutDir).TrimEnd('\',':')
    if((Get-PSDrive -Name $drive).Free -lt 10GB) {throw 'Output drive free space is below 10 GiB'}
}
function Read-Sql([string]$Query) {
    $raw=& docker @composeArgs exec -T postgres psql -X -q -U $dbUser -d $dbName -Atc $Query 2>&1
    if($LASTEXITCODE -ne 0) {throw 'Read-only performance database query failed'}
    return ($raw -join "`n").Trim()
}
function Assert-Quiet([int]$Workers=0) {
    $active=Read-Sql "BEGIN READ ONLY; SELECT count(*) FROM task_report.tasks WHERE state='running' OR (dispatch_version>0 AND state IN ('pending','failed')); COMMIT;"
    if([long]$active -ne 0) {throw 'Nonterminal tasks remain; no new tasks or database restore is permitted'}
    try {$queue=Invoke-RestMethod -Uri ("http://127.0.0.1:$rabbitPort/api/queues/%2F/task-report-worker") -Headers $rabbitHeader -TimeoutSec 10}
    catch {throw 'Could not verify RabbitMQ queue readiness through its read-only management endpoint'}
    if([int]$queue.messages_ready -ne 0 -or [int]$queue.messages_unacknowledged -ne 0) {throw 'RabbitMQ ready/unacked messages remain; wait for drain before restoring a checkpoint'}
    if($Workers -gt 0 -and [int]$queue.consumers -ne ($Workers*2)) {throw 'Observed consumer count differs from worker replicas x two slots'}
    Write-Event 'quiescence_verified' @{workers=$Workers;consumers=$queue.consumers;ready=$queue.messages_ready;unacked=$queue.messages_unacknowledged}
}
function Wait-Quiet([int]$Workers=0) {
    $deadline=[DateTimeOffset]::UtcNow.AddSeconds(60)
    do {
        try {Assert-Quiet $Workers;return} catch {$reason=$_.Exception.Message}
        Assert-Resources
        if([DateTimeOffset]::UtcNow -ge $deadline) {throw $reason}
        Start-Sleep -Seconds 2
    } while($true)
}
function Invoke-Database([string]$Operation,[string]$Name,[int]$Workers,[string]$LogPrefix) {
    Assert-Resources;Wait-Quiet
    $flags=@((Join-Path $OutDir 'telemetry/maintenance.flag'),(Join-Path $SnapshotRoot 'telemetry/maintenance.flag'))|Select-Object -Unique
    foreach($flag in $flags) {[void][IO.Directory]::CreateDirectory((Split-Path -Parent $flag));[IO.File]::WriteAllText($flag,"Job matrix $Operation $Name")}
    $log=Join-Path $suiteDir ($LogPrefix+'-'+$Operation+'.log')
    try {
        Write-Event ('database_'+$Operation+'_started') @{snapshot=$Name;workers=$Workers}
        & $databasePath -Mode $Operation -Name $Name -EnvFile $EnvFile -ComposeProject $ComposeProject -OutDir $SnapshotRoot -Workers $Workers *> $log
        Write-Event ('database_'+$Operation+'_finished') @{snapshot=$Name;workers=$Workers}
    } finally {foreach($flag in $flags) {if(Test-Path -LiteralPath $flag) {Remove-Item -LiteralPath $flag}}}
    Assert-Resources;Wait-Quiet $Workers
}
function Invoke-JobProcess([string]$Kind,[string]$Profile,[int]$Rows,[int]$Concurrency,[string]$RunId,[string]$Directory,[string]$Expected='') {
    Assert-Resources
    [void][IO.Directory]::CreateDirectory($Directory)
    $arguments=@('-NoProfile','-File',$jobsPath,'-Mode',$Kind,'-Profile',$Profile,'-Rows',[string]$Rows,'-Concurrency',[string]$Concurrency,'-Repetitions','1','-EnvFile',$EnvFile,'-ComposeProject',$ComposeProject,'-BaseUrl',$BaseUrl,'-OutDir',$Directory,'-RunId',$RunId,'-WaitSeconds',[string]$WaitSeconds)
    if($Kind -eq 'export') {$arguments+=@('-ExpectedRows',[string]$Rows,'-ExpectedCsvPath',$Expected)}
    $start=[Diagnostics.ProcessStartInfo]::new()
    $start.FileName=(Get-Process -Id $PID).Path
    $start.WorkingDirectory=$repoRoot;$start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.WindowStyle=[Diagnostics.ProcessWindowStyle]::Hidden
    $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    foreach($argument in $arguments) {$start.ArgumentList.Add($argument)}
    $process=[Diagnostics.Process]::new();$process.StartInfo=$start
    $stdout=$null;$stderr=$null;$launched=$false
    try {
        [void]$process.Start();$launched=$true;$stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
        $polls=0
        while(-not $process.WaitForExit(1000)) {
            $polls++
            if($polls -gt ($WaitSeconds+600)) {throw 'Client orchestration exceeded its task budget plus ten-minute preparation/verification allowance'}
            if(Test-Path -LiteralPath $abortPath) {$process.Kill($true);throw 'Collector requested stop; task evidence is retained and server tasks may still run'}
            if($polls%5 -eq 0) {Assert-Resources}
        }
        if($process.ExitCode -ne 0) {throw 'jobs.ps1 returned a failure; see its stdout/stderr and per-task evidence'}
        $file=Join-Path $Directory ($Kind+'-'+$RunId+'.manifest.json')
        if(-not(Test-Path -LiteralPath $file)) {throw 'jobs.ps1 completed without its manifest'}
        $manifest=Get-Content -Raw -LiteralPath $file|ConvertFrom-Json
        if($manifest.task_denominator -ne $Concurrency -or $manifest.passed -ne $Concurrency -or $manifest.failed_or_unfinished -ne 0) {throw 'Job manifest denominator or success counts failed'}
        if($Kind -eq 'import' -and @($manifest.tasks|Where-Object {-not $_.db_verified -or -not $_.replay_passed}).Count) {throw 'Import database/replay verification was incomplete'}
        if($Kind -eq 'export' -and @($manifest.tasks|Where-Object {-not $_.content_verified}).Count) {throw 'Export frozen-content verification was incomplete'}
        Wait-Quiet
        return @{manifest=$manifest;path=$file;directory=$Directory}
    } finally {
        if($launched -and -not $process.HasExited) {$process.Kill($true);$process.WaitForExit()}
        if($stdout) {[IO.File]::WriteAllText((Join-Path $Directory 'stdout.log'),$stdout.GetAwaiter().GetResult())}
        if($stderr) {[IO.File]::WriteAllText((Join-Path $Directory 'stderr.log'),$stderr.GetAwaiter().GetResult())}
        $process.Dispose()
    }
}
function Prepare-Export([int]$Rows) {
    $name="$RunPrefix-exp$Rows"
    if($checkpoints.ContainsKey([string]$Rows)) {
        $existing=$checkpoints[[string]$Rows]
        $dump=Join-Path $SnapshotRoot ($existing.snapshot+'.dump')
        if(-not(Test-Path -LiteralPath $dump) -or -not(Test-Path -LiteralPath $existing.expected_csv)) {throw 'Saved export checkpoint references a missing dump or frozen CSV'}
        if((Get-FileHash -LiteralPath $dump -Algorithm SHA256).Hash -ne $existing.dump_sha256 -or (Get-FileHash -LiteralPath $existing.expected_csv -Algorithm SHA256).Hash -ne $existing.csv_sha256) {throw 'Saved export checkpoint digest changed'}
        return
    }
    $directory=Join-Path $suiteDir ('prepare-n'+$Rows)
    [void][IO.Directory]::CreateDirectory($directory)
    $state.current=@{id="prepare-n$Rows";state='restoring';planned_new_tasks=2};Save-Matrix
    Invoke-Database restore $BaseSnapshot 1 ('prepare-n'+$Rows)
    $existingCount=Read-Sql 'BEGIN READ ONLY; SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL; COMMIT;'
    if([long]$existingCount -ne 0) {throw 'Export fixture preparation requires an empty responsible-department visible collection'}
    $import=Invoke-JobProcess import F1 $Rows 1 "$RunPrefix-prep-i$Rows" (Join-Path $directory 'import')
    $source=$import.manifest.tasks[0].source_csv
    $export=Invoke-JobProcess export F1 $Rows 1 "$RunPrefix-prep-e$Rows" (Join-Path $directory 'export') $source
    $frozen=Join-Path $export.directory ($export.manifest.tasks[0].label+'.csv')
    Invoke-Database snapshot $name 1 ('prepare-n'+$Rows)
    $checkpoint=[ordered]@{snapshot=$name;rows=$Rows;expected_csv=$frozen;csv_sha256=(Get-FileHash -LiteralPath $frozen -Algorithm SHA256).Hash;normalized_content_sha256=$export.manifest.tasks[0].download.normalized_content_sha256;dump_sha256=(Get-FileHash -LiteralPath (Join-Path $SnapshotRoot ($name+'.dump')) -Algorithm SHA256).Hash;import_manifest=$import.path;export_manifest=$export.path;created_utc=[DateTimeOffset]::UtcNow.ToString('o')}
    $checkpoints[[string]$Rows]=$checkpoint
    [IO.File]::WriteAllText($checkpointPath,($checkpoints|ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false))
    $records.Add(@{id="prepare-n$Rows";section='Prepare';state='passed';checkpoint=$checkpoint})
    Save-Matrix
}
Save-Matrix
try {
    Assert-Resources;Wait-Quiet
    if($Stage -in @('All','Prepare','S6','Scaling')) {foreach($count in @(10000,100000)) {Prepare-Export $count}}
    foreach($point in $selected) {
        $pointDir=Join-Path $suiteDir $point.id
        if(Test-Path -LiteralPath (Join-Path $pointDir 'point.json')) {throw 'An existing point evidence file prevents overwriting; use another RunPrefix or a new OutDir'}
        [void][IO.Directory]::CreateDirectory($pointDir)
        $record=[ordered]@{id=$point.id;section=$point.section;kind=$point.kind;rows=$point.rows;concurrency=$point.concurrency;round=$point.round;workers=$point.workers;slots_per_worker=2;snapshot=$point.snapshot;state='restoring';started_utc=[DateTimeOffset]::UtcNow.ToString('o');manifests=@();error=$null}
        $records.Add($record);$state.current=$record
        [IO.File]::WriteAllText((Join-Path $pointDir 'point.json'),($record|ConvertTo-Json -Depth 12))
        Save-Matrix
        Invoke-Database restore $point.snapshot $point.workers $point.id
        $record.state='running';Save-Matrix
        Write-Event 'point_started' $point
        if($point.kind -eq 'roundtrip') {
            $import=Invoke-JobProcess import F2 $point.rows 1 $point.run_id (Join-Path $pointDir 'import')
            $record.manifests+=@($import.path)
            $source=$import.manifest.tasks[0].source_csv
            $export=Invoke-JobProcess export F2 $point.rows 1 ($point.run_id+'-out') (Join-Path $pointDir 'export') $source
            $record.manifests+=@($export.path)
        } elseif($point.kind -eq 'export') {
            $checkpoint=$checkpoints[[string]$point.rows]
            $export=Invoke-JobProcess export F1 $point.rows $point.concurrency $point.run_id $pointDir $checkpoint.expected_csv
            $record.manifests+=@($export.path)
        } else {
            $import=Invoke-JobProcess import F1 $point.rows $point.concurrency $point.run_id $pointDir
            $record.manifests+=@($import.path)
        }
        $record.state='passed';$record.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
        [IO.File]::WriteAllText((Join-Path $pointDir 'point.json'),($record|ConvertTo-Json -Depth 12))
        $state.completed_batches++;Save-Matrix;Write-Event 'point_finished' $record
        Write-Output "File matrix passed: $($point.id)"
    }
    $state.status='passed';$state.current=$null
} catch {
    $state.status='stopped';$state.error=$_.Exception.Message
    if($state.current) {
        $state.current.state='failed_or_interrupted';$state.current.error=$state.error
        $state.current.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
        $interruptedDirectory=Join-Path $suiteDir $state.current.id
        if(Test-Path -LiteralPath $interruptedDirectory) {[IO.File]::WriteAllText((Join-Path $interruptedDirectory 'point.json'),($state.current|ConvertTo-Json -Depth 12))}
    }
    Write-Event 'matrix_stopped' @{error=$state.error;current=$state.current;note='No further tasks or restores were started. Existing server tasks and containers remain available for inspection.'}
    throw
} finally {
    $state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-Matrix
}
Write-Output "File matrix complete: $manifestPath"
