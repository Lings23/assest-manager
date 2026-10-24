#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/20261015-local-baseline',
    [string]$SeedManifest='',
    [string]$CandidatePath='',
    [string]$EnvFile='.env.perf',
    [string]$BaseUrl='http://127.0.0.1:59080/api/v1',
    [ValidateSet('asset-amqp-perf','asset-amqp-opt')][string]$ComposeProject='asset-amqp-perf',
    [ValidateSet('none','scoped','search')][string]$IndexProfile='none',
    [ValidateRange(1,2)][int]$Workers=1,
    [ValidateRange(1,4)][int]$Slots=2,
    [double]$OnlineRPS=0,
    [string]$FrozenSnapshotPath='',
    [string]$FrozenExportManifest='',
    [ValidatePattern('^[a-zA-Z0-9_-]{1,48}$')][string]$SnapshotName='s7base',
    [ValidatePattern('^[a-zA-Z0-9_.-]{1,24}$')][string]$RunPrefix=('s7-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmss')),
    [ValidateRange(1,3)][int]$Repetitions=1,
    [ValidateRange(4,256)][int]$MaxVUs=64,
    [ValidateRange(120,1800)][int]$WaitSeconds=1200,
    [ValidateRange(0.01,0.5)][double]$MinHostFreeMemoryRatio=0.15,
    [ValidateSet('control','statistics','import','export','both')][string[]]$Points=@('control','statistics','import','export','both'),
    [ValidateSet(1,2,4)][int]$ImportConcurrency=1,
    [switch]$ValidateOnly
)
$ErrorActionPreference='Stop'
if($Points[0] -ne 'control' -or @($Points|Select-Object -Unique).Count -ne $Points.Count){throw 'Each comparison needs a first, unique control case'}
if($ImportConcurrency -ne 1 -and ('both' -in $Points -or 'export' -in $Points)){throw 'Multi-import diagnostic uses only control/statistics/import points; original both overlap gate is single-import only'}
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$OutDir=[IO.Path]::GetFullPath($OutDir).TrimEnd('\','/')
$allowedRoot=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $OutDir.StartsWith($allowedRoot,[StringComparison]::OrdinalIgnoreCase)){throw 'OutDir must be within ignored repository data/perf'}
if(-not $SeedManifest){$SeedManifest=Join-Path $OutDir 'seed-d1/seed-manifest.json'}
if(-not $CandidatePath){$CandidatePath=Join-Path $OutDir 'api-20261016-candidate.json'}
$SeedManifest=(Resolve-Path -LiteralPath $SeedManifest).Path
$CandidatePath=(Resolve-Path -LiteralPath $CandidatePath).Path
$EnvFile=(Resolve-Path -LiteralPath $EnvFile).Path
$gateway=[Uri]$BaseUrl
if($gateway.Host -notin @('127.0.0.1','localhost','::1') -or $gateway.Port -ne 59080 -or
   $gateway.AbsolutePath.TrimEnd('/') -ne '/api/v1' -or $gateway.Query -or $gateway.UserInfo){throw 'S7 requires the isolated local Gateway on port 59080 at /api/v1'}
$manifest=Get-Content -LiteralPath $SeedManifest -Raw|ConvertFrom-Json -AsHashtable
[void]$manifest.Remove('assets');[void]$manifest.Remove('samples')
if(-not $ValidateOnly -and ($manifest.dataset -ne 'D1' -or $manifest.state -ne 'completed')){throw 'S7 requires a completed D1 manifest'}
if($manifest.base_url.TrimEnd('/') -ne $BaseUrl.TrimEnd('/')){throw 'Manifest and S7 Gateway differ'}
$credentials=Get-Content -LiteralPath $manifest.credentials_file -Raw|ConvertFrom-Json -AsHashtable
$candidate=Get-Content -LiteralPath $CandidatePath -Raw|ConvertFrom-Json -AsHashtable
if(-not $ValidateOnly -and (-not $candidate.all_repeats_passed -or [int]$candidate.three_repeats_completed -ne 3)){throw 'S7 requires exactly three passing comparable repetitions of the mixed candidate'}
$onlineRate=if($OnlineRPS -gt 0){$OnlineRPS}else{[double]$candidate.rps*0.6}
if($ComposeProject -eq 'asset-amqp-opt' -and $onlineRate -ne 48){throw 'Optimization S7 preserves the original 48 RPS regardless of a new capacity candidate'}
if(-not [double]::IsFinite($onlineRate) -or $onlineRate -le 0 -or [Math]::Abs($onlineRate*60-[Math]::Round($onlineRate*60)) -gt 0.000001){throw 'Candidate rps must produce a positive k6 arrival rate in multiples of 1/60 RPS'}
$envValues=@{}
foreach($line in [IO.File]::ReadAllLines($EnvFile)){if($line -match '^\s*([^#=]+)=(.*)$'){$envValues[$matches[1].Trim()]=$matches[2].Trim().Trim('"').Trim("'")}}
if($envValues.POSTGRES_DB -ne 'asset_perf' -or $envValues.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated performance database'}
if(-not $envValues.POSTGRES_USER){throw 'POSTGRES_USER must be explicit'}
$composeArgs=@('compose','--env-file',$EnvFile,'-p',$ComposeProject)
function Resolve-Account($User) {
    $credential=@($credentials.users|Where-Object{($_.id -eq $User.id -or $_.user_id -eq $User.id) -and $_.username -eq $User.username})
    if($credential.Count -ne 1 -or -not $credential[0].password){throw 'A selected fixture account lacks exactly one saved credential'}
    if($credential[0].password -match '[\r\n]' -or $credential[0].username -match '[\r\n]'){throw 'Credential cannot be represented in a temporary env file'}
    return @{id=$User.id;username=$User.username;roles=@($User.roles);data_scope=$User.data_scope;department_id=$User.department_id;password=$credential[0].password}
}
function Public-Account($Account){return @{id=$Account.id;username=$Account.username;roles=$Account.roles;data_scope=$Account.data_scope;department_id=$Account.department_id}}
$writers=@($manifest.users|Where-Object{$_.data_scope -eq 'own' -and ('reporter' -in $_.roles -or 'admin' -in $_.roles)}|Sort-Object username)
$reporters=@($writers|Where-Object{'reporter' -in $_.roles})
if($writers.Count -lt 3 -or $reporters.Count -lt 2){throw 'S7 requires two reporter own accounts and a third writable own account'}
$accountA=Resolve-Account $reporters[0]
$accountB=Resolve-Account $reporters[1]
$third=@($writers|Where-Object{$_.id -notin @($accountA.id,$accountB.id)}|Sort-Object @{Expression={if('admin' -in $_.roles){0}else{1}}},username)
$accountC=Resolve-Account $third[0]
$auditors=@($manifest.users|Where-Object{$_.data_scope -eq 'own' -and 'auditor' -in $_.roles -and $_.id -notin @($accountA.id,$accountB.id,$accountC.id)}|Sort-Object username)
if(-not $auditors.Count){throw 'S7 requires a distinct auditor own account'}
$accountD=Resolve-Account $auditors[0]
$selected=@($accountA,$accountB,$accountC,$accountD)
foreach($account in $selected){if($account.id -notmatch '^[a-fA-F0-9-]{36}$'){throw 'Invalid selected fixture user ID'}}
$expectedExportRows=[long]$manifest.per_user_counts[$accountB.id]['responsible-department']+100000
$plan=@{run_prefix=$RunPrefix;candidate_path=$CandidatePath;seed_manifest=$SeedManifest;candidate_three_repeats_passed=([bool]$candidate.all_repeats_passed -and [int]$candidate.three_repeats_completed -eq 3);candidate_rps=[double]$candidate.rps;online_rps=$onlineRate;stats_rps=0.2;warmup_seconds=60;steady_seconds=300;
    background_after_arrival_clock_seconds=120;accounts=@{online=(Public-Account $accountA);export=(Public-Account $accountB);import=(Public-Account $accountC);statistics=(Public-Account $accountD)};
    expected_frozen_export_rows=$expectedExportRows;points=$Points;control_stats_rps=0;repetitions=$Repetitions;max_vus=$MaxVUs;workers=$Workers;slots_per_worker=$Slots;index_profile=$IndexProfile;frozen_snapshot_source=$FrozenSnapshotPath;frozen_export_manifest=$FrozenExportManifest;min_host_free_memory_ratio=$MinHostFreeMemoryRatio;
    import_concurrency=$ImportConcurrency;isolation='All four accounts have own data scope and distinct owner IDs. No role or scope changes are performed.';
    validation_timing='Measured import SQL verification runs after online/statistics clients finish. Client CSV validation, sample reads and replay remain part of the co-located load driver.'}
if($ValidateOnly){return [pscustomobject]$plan}
if(-not(Test-Path -LiteralPath (Join-Path $OutDir 'd1.dump'))){throw 'The durable d1.dump is required'}
if(-not $FrozenSnapshotPath -and (Test-Path -LiteralPath (Join-Path $OutDir "$SnapshotName.dump"))){throw 'S7 snapshot already exists; choose a new SnapshotName after inspecting earlier task state'}
if([bool]$FrozenSnapshotPath -ne [bool]$FrozenExportManifest){throw 'Frozen snapshot and its successful export manifest must be provided together'}
$suiteDir=Join-Path $OutDir "interference/$RunPrefix"
if(Test-Path -LiteralPath $suiteDir){throw 'S7 evidence directory already exists; inspect it before starting another suite'}
$null=New-Item -ItemType Directory -Path $suiteDir -Force
$abortFile=Join-Path $OutDir 'telemetry/abort.json'
$eventFile=Join-Path $suiteDir 'events.jsonl'
$stateFile=Join-Path $suiteDir 'state.json'
$pwshPath=(Get-Process -Id $PID).Path
$children=[Collections.Generic.List[object]]::new()
$cases=[Collections.Generic.List[object]]::new()
$script:s7LastResourceCheck=[DateTimeOffset]::MinValue
$script:s7LastProcessInventory=[DateTimeOffset]::MinValue
$script:s7ProcessInventory=@()
$state=@{schema_version=1;run_id=$RunPrefix;started_utc=[DateTimeOffset]::UtcNow.ToString('o');status='preparing';plan=$plan;cases=@();preparation=@{};error=$null;
    scripts=@(foreach($file in @('interference.ps1','api.js','run.ps1','jobs.ps1','database.ps1','verify.sql')){@{name=$file;sha256=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot $file) -Algorithm SHA256).Hash.ToLowerInvariant()}})}
function Save-Json($Value,[string]$Path){[IO.File]::WriteAllText($Path,($Value|ConvertTo-Json -Depth 40),[Text.UTF8Encoding]::new($false))}
function Save-State {$state.cases=$cases.ToArray();Save-Json $state $stateFile}
function Event([string]$Kind,$Details){@{utc=[DateTimeOffset]::UtcNow.ToString('o');kind=$Kind;details=$Details}|ConvertTo-Json -Depth 15 -Compress|Add-Content -LiteralPath $eventFile -Encoding utf8}
function Assert-Ready {
    if(Test-Path -LiteralPath $abortFile){throw 'Collector requested a stop; no new work or restore is permitted'}
    if(([DateTimeOffset]::UtcNow-$script:s7LastResourceCheck).TotalSeconds -ge 5){
        $os=Get-CimInstance Win32_OperatingSystem
        if([double]$os.FreePhysicalMemory/[double]$os.TotalVisibleMemorySize -lt $MinHostFreeMemoryRatio){throw "Host free memory is below $($MinHostFreeMemoryRatio*100)%"}
        if((Get-PSDrive -Name D).Free -lt 10GB){throw 'D drive free space is below 10 GiB'}
        $script:s7LastResourceCheck=[DateTimeOffset]::UtcNow
    }
}
function Quote-ChildArgument([string]$Value){
    if($Value -match '["\r\n]'){throw 'Unsupported child argument characters'}
    return '"'+$Value.TrimEnd('\')+'"'
}
function Start-Child([string]$Name,[string[]]$Arguments,[string]$EvidenceDir){
    Assert-Ready
    $null=New-Item -ItemType Directory -Path $EvidenceDir -Force
    $stdout=Join-Path $EvidenceDir "$Name.stdout.log";$stderr=Join-Path $EvidenceDir "$Name.stderr.log"
    $argumentText=(@('-NoProfile','-File')+$Arguments|ForEach-Object{Quote-ChildArgument $_}) -join ' '
    # A preceding S2 invocation may leave optional selectors in the caller's
    # environment. Child workloads use their script defaults and explicit args.
    $savedOptional=@{}
    foreach($key in @('SEARCH_QUERY','SEARCH_MATCH','SEARCH_EXPECT_MIN','SEARCH_EXPECT_MAX','OFFSET','LIMIT','WINDOW_SECONDS','PRE_ALLOCATED_VUS')){
        $savedOptional[$key]=[Environment]::GetEnvironmentVariable($key,'Process')
        [Environment]::SetEnvironmentVariable($key,$null,'Process')
    }
    try{$process=Start-Process -FilePath $pwshPath -ArgumentList $argumentText -WorkingDirectory $repoRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr}
    finally{foreach($key in $savedOptional.Keys){[Environment]::SetEnvironmentVariable($key,$savedOptional[$key],'Process')}}
    $record=@{name=$Name;process=$process;pid=$process.Id;started_utc=[DateTimeOffset]::UtcNow.ToString('o');stdout=$stdout;stderr=$stderr;finished_utc=$null;exit_code=$null;descendants=@{}}
    $children.Add($record)
    Event 'child_started' @{name=$Name;pid=$process.Id;stdout=$stdout;stderr=$stderr}
    return $record
}
function Observe-Children {
    if(([DateTimeOffset]::UtcNow-$script:s7LastProcessInventory).TotalSeconds -ge 3){
        $script:s7ProcessInventory=@(Get-CimInstance Win32_Process -Property ProcessId,ParentProcessId,Name,CreationDate)
        $script:s7LastProcessInventory=[DateTimeOffset]::UtcNow
    }
    foreach($record in $children){
        $record.process.Refresh()
        if(-not $record.process.HasExited){
            $ids=@([int]$record.pid)
            for($changed=$true;$changed;){
                $changed=$false
                foreach($p in $script:s7ProcessInventory){if([int]$p.ParentProcessId -in $ids -and [int]$p.ProcessId -notin $ids -and [DateTimeOffset]$p.CreationDate -ge [DateTimeOffset]$record.started_utc){$ids+=[int]$p.ProcessId;$record.descendants[[string]$p.ProcessId]=@{pid=[int]$p.ProcessId;name=$p.Name;created=$p.CreationDate};$changed=$true}}
            }
        }
        if($record.process.HasExited -and $null -eq $record.exit_code){
            $record.exit_code=$record.process.ExitCode;$record.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
            Event 'child_finished' @{name=$record.name;pid=$record.pid;exit_code=$record.exit_code}
        }
        if($null -ne $record.exit_code -and $record.exit_code -ne 0){throw "Child $($record.name) failed; inspect its preserved logs"}
    }
}
function Tick([int]$Milliseconds=1000){Assert-Ready;Observe-Children;Start-Sleep -Milliseconds $Milliseconds}
function Wait-Children($Records,[int]$Seconds){
    $deadline=[DateTimeOffset]::UtcNow.AddSeconds($Seconds)
    do{
        Assert-Ready;Observe-Children
        if(@($Records|Where-Object{$null -eq $_.exit_code}).Count -eq 0){return}
        if([DateTimeOffset]::UtcNow -ge $deadline){throw 'Child wait budget exhausted; server tasks may still be running'}
        Start-Sleep -Seconds 1
    }while($true)
}
function Invoke-DatabaseScript([string]$Mode,[string]$Name){
    if(@($children|Where-Object{$null -eq $_.exit_code}).Count){throw 'Cannot change a snapshot while a child is still running'}
    $record=Start-Child "$Mode-$Name" @((Join-Path $PSScriptRoot 'database.ps1'),'-Mode',$Mode,'-Name',$Name,'-EnvFile',$EnvFile,'-ComposeProject',$ComposeProject,'-OutDir',$OutDir,'-Workers',"$Workers",'-IndexProfile',$IndexProfile) $suiteDir
    Wait-Children @($record) 300
}
function Read-Database([string]$Select){
    $sql="BEGIN READ ONLY; SET LOCAL statement_timeout='15s'; $Select; COMMIT;"
    $native=@(& docker @composeArgs exec -T postgres psql -U $envValues.POSTGRES_USER -d $envValues.POSTGRES_DB -XqAt -v ON_ERROR_STOP=1 -c $sql 2>&1)
    if($LASTEXITCODE -ne 0){throw 'Read-only database observation failed'}
    try{return (($native -join "`n")|ConvertFrom-Json -AsHashtable)}catch{throw 'Read-only database observation did not return one JSON value'}
}
function Get-Quiescence {return Read-Database "SELECT json_build_object('active',(SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state IN ('pending','running','failed')),'unpublished',(SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL))"}
function Assert-Quiescent {
    $q=Get-Quiescence
    if([long]$q.active -ne 0 -or [long]$q.unpublished -ne 0){throw 'Nonterminal tasks or unpublished events remain; no snapshot/restore/new work is permitted'}
}
function Get-Counts {
    return Read-Database @"
WITH ac AS (SELECT owner_id::text AS owner,count(*) AS rows,count(*) FILTER(WHERE deleted_at IS NULL) AS active FROM asset.responsible_department_assets GROUP BY owner_id),
vc AS (SELECT a.owner_id::text AS owner,count(*) AS versions FROM asset.asset_versions v JOIN asset.responsible_department_assets a ON a.id=v.asset_id WHERE v.asset_type='responsible-department' GROUP BY a.owner_id)
SELECT json_build_object('rows',(SELECT count(*) FROM asset.responsible_department_assets),'active',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'versions',(SELECT count(*) FROM asset.asset_versions WHERE asset_type='responsible-department'),'owners',(SELECT json_object_agg(ac.owner,json_build_object('rows',ac.rows,'active',ac.active,'versions',COALESCE(vc.versions,0))) FROM ac LEFT JOIN vc ON ac.owner=vc.owner))
"@
}
function Get-TaskStates([string[]]$IDs){
    if(-not $IDs.Count){return @()}
    foreach($id in $IDs){if($id -notmatch '^[a-fA-F0-9-]{36}$'){throw 'Invalid task ID for read-only observation'}}
    $literals=($IDs|ForEach-Object{"'$_'::uuid"}) -join ','
    return @(Read-Database "SELECT COALESCE(json_agg(json_build_object('id',id::text,'kind',kind,'state',state,'attempt',attempt,'created_at',created_at,'updated_at',updated_at,'requested_by',requested_by::text)),'[]'::json) FROM task_report.tasks WHERE id IN ($literals)")
}
function Write-AccountEnv([string]$Label,$Account){
    $path=Join-Path $suiteDir "account-$Label.env"
    $text=[IO.File]::ReadAllText($EnvFile).TrimEnd()+"`nPERF_ADMIN_USERNAME=$($Account.username)`nPERF_ADMIN_PASSWORD=$($Account.password)`n"
    [IO.File]::WriteAllText($path,$text,[Text.UTF8Encoding]::new($false))
    return $path
}
function Start-Job([string]$Name,[string]$Mode,[string]$JobEnv,[string]$EvidenceDir,[string]$RunId,[string]$FrozenCsv='',[switch]$DeferDatabaseVerify){
    $jobConcurrency=if($Mode -eq 'import'){$ImportConcurrency}else{1}
    $jobArguments=@((Join-Path $PSScriptRoot 'jobs.ps1'),'-Mode',$Mode,'-BaseUrl',$BaseUrl,'-EnvFile',$JobEnv,'-OutDir',$EvidenceDir,'-RunId',$RunId,'-Rows','100000','-Concurrency',"$jobConcurrency",'-Repetitions','1','-AssetType','responsible-department','-Profile','F1','-ComposeProject',$ComposeProject,'-WaitSeconds',"$WaitSeconds")
    if($Mode -eq 'export'){$jobArguments+=@('-ExpectedRows',"$expectedExportRows",'-RequiredOwnerId',$accountB.id);if($FrozenCsv){$jobArguments+=@('-ExpectedCsvPath',$FrozenCsv)}}
    if($DeferDatabaseVerify){$jobArguments+=@('-SkipDatabaseVerify')}
    $child=Start-Child $Name $jobArguments $EvidenceDir
    $child.job_mode=$Mode;$child.job_dir=$EvidenceDir;$child.job_run_id=$RunId
    $child.job_expected_tasks=$jobConcurrency
    $child.job_manifest=Join-Path $EvidenceDir "$Mode-$RunId.manifest.json"
    $child.job_item=Join-Path $EvidenceDir "$Mode-$Mode-F1-100000-c1-r1-s1-$RunId.json"
    return $child
}
function Read-JobItem($Record){
    if(-not(Test-Path -LiteralPath $Record.job_item)){return $null}
    try{return Get-Content -LiteralPath $Record.job_item -Raw|ConvertFrom-Json -AsHashtable}catch{return $null}
}
function Require-JobSuccess($Record){
    if(-not(Test-Path -LiteralPath $Record.job_manifest)){throw 'Job ended without a manifest'}
    $result=Get-Content -LiteralPath $Record.job_manifest -Raw|ConvertFrom-Json -AsHashtable
    if($result.task_denominator -ne $Record.job_expected_tasks -or $result.passed -ne $Record.job_expected_tasks -or $result.failed_or_unfinished -ne 0 -or @($result.tasks|Where-Object{$_.state -ne 'succeeded' -or $_.attempt -ne 1}).Count){throw 'A file task failed, retried or remains unfinished; no subsequent work is permitted'}
    return $result
}
function Assert-PostImportCounts($Row,[string]$TaskID,[long]$ExpectedRows){
    if($Row.task_id -ne $TaskID){throw 'Post-online SQL evidence refers to another task'}
    foreach($field in @('import_keys','parsed_rows','committed_rows','active_assets','import_versions','reported_committed','reported_created')){
        if([long]$Row.$field -ne $ExpectedRows){throw "Post-online import SQL count mismatch: $field"}
    }
    if([long]$Row.same_key_tasks -ne 1 -or [long]$Row.reported_skipped -ne 0 -or [long]$Row.dead_letters -ne 0 -or [long]$Row.unpublished_events -ne 0){throw 'Post-online task idempotency/outbox verification failed'}
}
function Verify-ImportAfterOnline([string]$TaskID,[long]$ExpectedRows,[string]$OutputPath){
    if($TaskID -notmatch '^[a-fA-F0-9-]{36}$'){throw 'Invalid post-online task UUID'}
    $sql=[IO.File]::ReadAllText((Join-Path $PSScriptRoot 'verify.sql'))
    $native=@($sql|& docker @composeArgs exec -T postgres psql -X -q --csv -v ON_ERROR_STOP=1 -U $envValues.POSTGRES_USER -d $envValues.POSTGRES_DB -v ('task_ids_json=["'+$TaskID+'"]') 2>&1)
    if($LASTEXITCODE -ne 0){throw 'Post-online read-only import SQL verification failed'}
    $text=$native -join "`n";[IO.File]::WriteAllText($OutputPath,$text,[Text.UTF8Encoding]::new($false))
    $rows=@($text|ConvertFrom-Csv)
    if($rows.Count -ne 1){throw 'Post-online import evidence did not return exactly one task'}
    Assert-PostImportCounts $rows[0] $TaskID $ExpectedRows
    return @{task_id=$TaskID;rows=$ExpectedRows;passed=$true;path=$OutputPath;utc=[DateTimeOffset]::UtcNow.ToString('o');timing='After both API clients exited, before any subsequent restore'}
}
function Start-Api([string]$Name,[string]$Mode,[double]$Rate,$Account,[string]$Prefix){
    $apiArguments=@((Join-Path $PSScriptRoot 'run.ps1'),'-SeedManifest',$SeedManifest,'-Mode',$Mode,'-Rates',$Rate.ToString([Globalization.CultureInfo]::InvariantCulture),'-WarmupSeconds','60','-DurationSeconds','300','-Repetitions','1','-RunPrefix',$Prefix,'-OutDir',$OutDir,'-BaseUrl',$BaseUrl,'-EnvFile',$EnvFile,'-ComposeProject',$ComposeProject,'-IndexProfile',$IndexProfile,'-AccountScope','own','-AccountUsername',$Account.username,'-MaxVUs',"$MaxVUs")
    if($Mode -eq 'mixed'){$apiArguments+=@('-AssetType','responsible-department')}
    $record=Start-Child $Name $apiArguments $suiteDir
    $label=$Rate.ToString('0.###',[Globalization.CultureInfo]::InvariantCulture).Replace('.','p')
    $record.api_summary=Join-Path $OutDir "api/$Prefix-$Mode-$label-r1-summary.json"
    $record.api_runs=Join-Path $OutDir "api/$Prefix-$Mode-runs.json"
    $record.api_run_id="$Prefix-$Mode-$label-r1"
    $record.api_mode=$Mode;$record.api_owner_id=$Account.id
    return $record
}
function Require-ApiSuccess($Record){
    if(-not(Test-Path -LiteralPath $Record.api_summary)){throw 'API child ended without summary'}
    if(-not(Test-Path -LiteralPath $Record.api_runs)){throw 'API child ended without its final orchestration verdict'}
    $result=Get-Content -LiteralPath $Record.api_summary -Raw|ConvertFrom-Json -AsHashtable
    $runs=@(Get-Content -LiteralPath $Record.api_runs -Raw|ConvertFrom-Json -AsHashtable)
    if($runs.Count -ne 1 -or $runs[0].run_id -ne $Record.api_run_id -or $null -eq $runs[0].exit_code -or [int]$runs[0].exit_code -ne 0 -or -not $runs[0].load_point_passed -or -not $result.load_point_passed){throw 'API final verdict did not pass; no subsequent work or restore is permitted'}
    if($Record.api_mode -eq 'mixed'){
        $auditPath=$runs[0].write_audit
        if(-not $auditPath -or -not(Test-Path -LiteralPath $auditPath)){throw 'Online API lacks a final owner-scoped SQL write audit'}
        $audit=Get-Content -LiteralPath $auditPath -Raw|ConvertFrom-Json -AsHashtable
        if(-not $audit.passed -or $audit.owner_id -ne $Record.api_owner_id -or $audit.run_id -ne $Record.api_run_id){throw 'Online owner-scoped SQL write audit failed or refers to another run/owner'}
    }
    return $result
}
function Get-ExecutionEvidence([string]$Since,[string[]]$IDs){
    if(-not $IDs.Count){return @()}
    $raw=@(& docker @composeArgs logs --no-color --no-log-prefix --since $Since task-worker 2>&1)
    if($LASTEXITCODE -ne 0){return @()}
    $out=@()
    foreach($line in $raw){
        $text=[string]$line;$start=$text.IndexOf('{');if($start -lt 0){continue}
        try{$entry=$text.Substring($start)|ConvertFrom-Json -AsHashtable}catch{continue}
        if($entry.task_id -notin $IDs -or $entry.msg -ne 'task succeeded' -or $null -eq $entry.duration_ms){continue}
        # ConvertFrom-Json can turn some ISO precision variants into DateTime;
        # string coercion then loses offset/fractions. Parse the raw UTC text.
        $rawTime=[regex]::Match($text.Substring($start),'"time"\s*:\s*"([^"]+)"')
        if(-not $rawTime.Success){continue}
        $end=[DateTimeOffset]::Parse($rawTime.Groups[1].Value).ToUniversalTime()
        $out+=@{task_id=$entry.task_id;message=$entry.msg;worker_id=$entry.worker_id;completed_log_utc=$end.ToString('o');duration_ms=$entry.duration_ms;estimated_started_utc=$end.AddMilliseconds(-[double]$entry.duration_ms).ToString('o');note='Execution interval estimated from successful worker completion log and rounded duration_ms; no precise queue-time claim.'}
    }
    return $out
}
function Public-Child($Record){return @{name=$Record.name;pid=$Record.pid;started_utc=$Record.started_utc;finished_utc=$Record.finished_utc;exit_code=$Record.exit_code;stdout=$Record.stdout;stderr=$Record.stderr;api_summary=$Record.api_summary;api_runs=$Record.api_runs;api_owner_id=$Record.api_owner_id;job_manifest=$Record.job_manifest}}
function Compare-Cases {
    $comparisons=@()
    foreach($round in 1..$Repetitions){
        $control=@($cases|Where-Object{$_.round -eq $round -and $_.point -eq 'control'})[0]
        $controlSummaryPath=@($control.children|Where-Object{$_.name -like '*-online'})[0].api_summary
        $controlSummary=Get-Content -LiteralPath $controlSummaryPath -Raw|ConvertFrom-Json -AsHashtable
        foreach($tested in @($cases|Where-Object{$_.round -eq $round -and $_.point -ne 'control'})){
            $testedSummaryPath=@($tested.children|Where-Object{$_.name -like '*-online'})[0].api_summary
            $testedSummary=Get-Content -LiteralPath $testedSummaryPath -Raw|ConvertFrom-Json -AsHashtable
            $operations=@{}
            foreach($operation in $controlSummary.operations.Keys){
                $baseline=$controlSummary.operations[$operation];$measured=$testedSummary.operations[$operation]
                $growth=if([double]$baseline.duration_ms.'p(95)' -gt 0){[double]$measured.duration_ms.'p(95)'/[double]$baseline.duration_ms.'p(95)'-1}else{$null}
                $sufficient=([long]$baseline.successes -ge 100 -and [long]$measured.successes -ge 100)
                $windowValues=@()
                foreach($window in @($testedSummary.windows|Where-Object{$_.segment -eq 'steady'})){
                    $reference=@($controlSummary.windows|Where-Object{$_.segment -eq 'steady' -and $_.index -eq $window.index})
                    if($reference.Count -ne 1){continue}
                    $beforeValues=$reference[0].operations[$operation];$afterValues=$window.operations[$operation]
                    $windowGrowth=if([double]$beforeValues.'p(95)' -gt 0){[double]$afterValues.'p(95)'/[double]$beforeValues.'p(95)'-1}else{$null}
                    $windowValues+=@{window_index=$window.index;scheduled_seconds=$window.scheduled_seconds;control_count=$beforeValues.count;measured_count=$afterValues.count;
                        control_p95_ms=$beforeValues.'p(95)';measured_p95_ms=$afterValues.'p(95)';p95_growth_fraction=$windowGrowth;
                        p95_sample_sufficient=([long]$beforeValues.count -ge 100 -and [long]$afterValues.count -ge 100)}
                }
                $operations[$operation]=@{control_p95_ms=$baseline.duration_ms.'p(95)';measured_p95_ms=$measured.duration_ms.'p(95)';p95_growth_fraction=$growth;
                    control_successes=$baseline.successes;measured_successes=$measured.successes;p95_sample_sufficient=$sufficient;
                    within_30_percent=if($sufficient -and $null -ne $growth){$growth -le 0.3}else{$null};windows=$windowValues}
            }
            $comparisons+=@{round=$round;point=$tested.point;control_case=$control.case_id;measured_case=$tested.case_id;control_summary=$controlSummaryPath;
                measured_summary=$testedSummaryPath;operations=$operations;note='Compare identical nominal steady-window positions. Setup/start polling offsets are approximate; worker observations separately establish overlap. No queue-time estimate or pooled percentile is inferred.'}
        }
    }
    return $comparisons
}
function Stop-Children {
    foreach($record in $children){
        try{
            $record.process.Refresh()
            if(-not $record.process.HasExited){& taskkill.exe /PID $record.pid /T /F *> (Join-Path $suiteDir ("stop-$($record.pid).log"))}
            foreach($tracked in $record.descendants.Values){
                $p=Get-CimInstance Win32_Process -Filter "ProcessId=$($tracked.pid)" -ErrorAction SilentlyContinue
                if($p -and $p.Name -eq $tracked.name -and $p.CreationDate -eq $tracked.created){Stop-Process -Id $tracked.pid -ErrorAction SilentlyContinue}
            }
        }catch{Event 'child_stop_gap' @{name=$record.name;pid=$record.pid;error='Could not verify or stop every tracked child; inspect remaining process IDs'}}
    }
}
Save-State
$priorSlots=[Environment]::GetEnvironmentVariable('WORKER_SLOTS','Process')
$env:WORKER_SLOTS="$Slots"
try {
    Assert-Ready;Assert-Quiescent
    $envB=Write-AccountEnv 'B-export' $accountB;$envC=Write-AccountEnv 'C-import' $accountC
    if($FrozenSnapshotPath){
        $snapshotSource=(Resolve-Path -LiteralPath $FrozenSnapshotPath).Path
        $exportSource=(Resolve-Path -LiteralPath $FrozenExportManifest).Path
        $frozen=Get-Content -LiteralPath $exportSource -Raw|ConvertFrom-Json -AsHashtable
        if($frozen.passed -ne 1 -or $frozen.failed_or_unfinished -ne 0 -or $frozen.tasks.Count -ne 1 -or $frozen.tasks[0].download.rows -ne $expectedExportRows){throw 'Original frozen export manifest is not a successful matching export'}
        $frozenCSV=Join-Path (Split-Path -Parent $exportSource) ($frozen.tasks[0].label+'.csv')
        if(-not(Test-Path -LiteralPath $frozenCSV) -or (Get-FileHash -LiteralPath $frozenCSV).Hash.ToLowerInvariant() -ne $frozen.tasks[0].download.sha256){throw 'Original frozen CSV bytes do not match their archived export'}
        $snapshotTarget=Join-Path $OutDir "$SnapshotName.dump"
        $snapshotHash=(Get-FileHash -LiteralPath $snapshotSource).Hash
        if(Test-Path -LiteralPath $snapshotTarget){if((Get-FileHash -LiteralPath $snapshotTarget).Hash -ne $snapshotHash){throw 'Existing destination snapshot differs; no overwrite is allowed'}}else{Copy-Item -LiteralPath $snapshotSource -Destination $snapshotTarget}
        Invoke-DatabaseScript restore $SnapshotName
        $preparedCounts=Get-Counts
        if([long]$preparedCounts.owners[$accountB.id].active -ne $expectedExportRows){throw 'Frozen snapshot and selected export owner do not match'}
        $state.preparation=@{after=$preparedCounts;frozen_csv=$frozenCSV;frozen_rows=$expectedExportRows;frozen_digest=$frozen.tasks[0].download.normalized_content_sha256;snapshot=$SnapshotName;snapshot_sha256=$snapshotHash;source_snapshot=$snapshotSource;source_export_manifest=$exportSource;method='Reused immutable original S7 snapshot and byte-verified frozen export; no new preparation data'}
    }else{
    Invoke-DatabaseScript restore 'd1'
    $state.preparation.before=Get-Counts;Save-State
    $prepareImport=Start-Job 'prepare-B-import' 'import' $envB (Join-Path $suiteDir 'prepare-import') "$RunPrefix-prep-i"
    Wait-Children @($prepareImport) ($WaitSeconds+90)
    $prepareImportResult=Require-JobSuccess $prepareImport
    Assert-Quiescent
    $prepareExport=Start-Job 'freeze-B-export' 'export' $envB (Join-Path $suiteDir 'prepare-export') "$RunPrefix-prep-e"
    Wait-Children @($prepareExport) ($WaitSeconds+90)
    $prepareExportResult=Require-JobSuccess $prepareExport
    $frozenCSV=Join-Path $prepareExport.job_dir ($prepareExportResult.tasks[0].label+'.csv')
    if(-not(Test-Path -LiteralPath $frozenCSV) -or $prepareExportResult.tasks[0].download.rows -ne $expectedExportRows -or $prepareExportResult.tasks[0].download.unexpected_owners -ne 0){throw 'Frozen B export was not reconciled'}
    $preparedCounts=Get-Counts
    if([long]$preparedCounts.rows-[long]$state.preparation.before.rows -ne 100000 -or [long]$preparedCounts.versions-[long]$state.preparation.before.versions -ne 100000){throw 'S7 preparation changed an unexpected asset/version count'}
    $state.preparation=@{before=$state.preparation.before;after=$preparedCounts;import_manifest=$prepareImport.job_manifest;export_manifest=$prepareExport.job_manifest;frozen_csv=$frozenCSV;frozen_rows=$expectedExportRows;frozen_digest=$prepareExportResult.tasks[0].download.normalized_content_sha256;snapshot=$SnapshotName}
    Assert-Quiescent;Invoke-DatabaseScript snapshot $SnapshotName
    }
    $state.status='running';Save-State
    foreach($round in 1..$Repetitions){foreach($point in $Points){
        Assert-Ready;Assert-Quiescent
        Invoke-DatabaseScript restore $SnapshotName
        $before=Get-Counts
        $caseId="$RunPrefix-$point-r$round"
        $caseDir=Join-Path $suiteDir "$point-r$round";$null=New-Item -ItemType Directory -Path $caseDir -Force
        $case=@{case_id=$caseId;point=$point;round=$round;statistics_rps=$(if($point -eq 'control'){0}else{0.2});started_utc=[DateTimeOffset]::UtcNow.ToString('o');status='running';before=$before;task_observations=@();children=@();audit=$null;overlap=$null}
        $cases.Add($case);Save-State
        $online=Start-Api "$caseId-online" 'mixed' $onlineRate $accountA "$caseId-a"
        # The arrival clock starts after k6 setup. Observe completion of its
        # private fixtures rather than counting setup seconds as warmup.
        $expectedFixtures=$MaxVUs*2
        $fixtureDeadline=[DateTimeOffset]::UtcNow.AddSeconds(120)
        do{
            Tick 500
            $fixtureCounts=Get-Counts
            if([long]$fixtureCounts.owners[$accountA.id].rows-[long]$before.owners[$accountA.id].rows -ge $expectedFixtures){break}
            if([DateTimeOffset]::UtcNow -ge $fixtureDeadline){throw 'Online setup did not produce the expected private VU fixtures'}
        }while($true)
        $clockObserved=[DateTimeOffset]::UtcNow
        $case.arrival_clock_observed_utc=$clockObserved.ToString('o')
        $case.arrival_clock_note='Approximate start observed through completion of expected setup fixtures; polling/setup overhead is retained, not an exact k6 clock timestamp.'
        $stats=$null
        if($point -ne 'control'){$stats=Start-Api "$caseId-statistics" 'reporting' 0.2 $accountD "$caseId-d"}
        while([DateTimeOffset]::UtcNow -lt $clockObserved.AddSeconds(120)){Tick}
        $case.background_trigger_utc=[DateTimeOffset]::UtcNow.ToString('o')
        $inflight=@($online);if($stats){$inflight+=,$stats};$importChild=$null;$exportChild=$null
        if($point -in @('import','both')){
            $importChild=Start-Job "$caseId-import" 'import' $envC (Join-Path $caseDir 'import') "$caseId-i" -DeferDatabaseVerify
            $inflight+=,$importChild
        }
        if($point -eq 'both'){
            $gateDeadline=[DateTimeOffset]::UtcNow.AddSeconds(90)
            do{
                Tick 250
                $item=Read-JobItem $importChild
                if($item -and $item.task_id){
                    $observation=@(Get-TaskStates @($item.task_id))
                    $case.task_observations+=@{utc=[DateTimeOffset]::UtcNow.ToString('o');stage='wait_import_running';tasks=$observation}
                    if($observation.Count -eq 1 -and $observation[0].state -eq 'running' -and $observation[0].attempt -ge 1){$case.import_running_gate_utc=[DateTimeOffset]::UtcNow.ToString('o');break}
                    if(@($observation|Where-Object{$_.state -in @('succeeded','dead_letter','cancelled','failed')}).Count){throw 'Import execution gate was missed or failed; a later sequential export would not prove overlap'}
                }
                if([DateTimeOffset]::UtcNow -ge $gateDeadline){throw 'Import did not enter observable running state within overlap gate budget'}
            }while($true)
        }
        if($point -in @('export','both')){
            $exportChild=Start-Job "$caseId-export" 'export' $envB (Join-Path $caseDir 'export') "$caseId-e" $frozenCSV
            $inflight+=,$exportChild
        }
        $case.children=@($inflight|ForEach-Object{Public-Child $_});Save-State
        $caseDeadline=[DateTimeOffset]::UtcNow.AddSeconds($WaitSeconds+90)
        do{
            Tick 500
            if($point -eq 'both' -and -not $case.concurrent_running_observed_utc -and ($null -eq $importChild.exit_code -or $null -eq $exportChild.exit_code)){
                $i=Read-JobItem $importChild;$e=Read-JobItem $exportChild
                if($i.task_id -and $e.task_id){
                    $observation=@(Get-TaskStates @($i.task_id,$e.task_id))
                    $case.task_observations+=@{utc=[DateTimeOffset]::UtcNow.ToString('o');stage='observe_both';tasks=$observation}
                    if(@($observation|Where-Object{$_.state -eq 'running' -and $_.attempt -ge 1}).Count -eq 2){$case.concurrent_running_observed_utc=[DateTimeOffset]::UtcNow.ToString('o')}
                }
            }
            if(@($inflight|Where-Object{$null -eq $_.exit_code}).Count -eq 0){break}
            if([DateTimeOffset]::UtcNow -ge $caseDeadline){throw 'Case wait budget exhausted; no subsequent restore is permitted'}
        }while($true)
        $onlineSummary=Require-ApiSuccess $online;$statsSummary=$null
        if($stats){$statsSummary=Require-ApiSuccess $stats}
        $taskIDs=@()
        if($importChild){
            $r=Require-JobSuccess $importChild;$taskIDs+=@($r.tasks|ForEach-Object{$_.task_id})
            $case.import_sql_after_online=@(foreach($task in $r.tasks){Verify-ImportAfterOnline $task.task_id 100000 (Join-Path $caseDir ("import-sql-after-online-$($task.task_id).csv"))})
        }
        if($exportChild){$r=Require-JobSuccess $exportChild;$taskIDs+=$r.tasks[0].task_id}
        Assert-Quiescent
        $after=Get-Counts
        $addedA=[long]$onlineSummary.write_effects.fixture_creates+[long]$onlineSummary.write_effects.successful_creates_including_warmup
        $versionsA=[long]$onlineSummary.write_effects.expected_added_versions
        $addedC=if($importChild){100000*$ImportConcurrency}else{0}
        $audit=@{before=$before;after=$after;online_summary=$online.api_summary;statistics_summary=$(if($stats){$stats.api_summary}else{$null});expected_A_added_assets=$addedA;expected_A_added_versions=$versionsA;expected_C_import_assets=$addedC;checks=@{};passed=$false}
        $audit.checks.whole_assets=([long]$after.rows-[long]$before.rows -eq $addedA+$addedC)
        $audit.checks.whole_active=([long]$after.active-[long]$before.active -eq $addedA+$addedC)
        $audit.checks.whole_versions=([long]$after.versions-[long]$before.versions -eq $versionsA+$addedC)
        foreach($pair in @(@('A',$accountA,$addedA,$versionsA),@('B',$accountB,0,0),@('C',$accountC,$addedC,$addedC),@('D',$accountD,0,0))){
            $audit.checks["$($pair[0])_assets"]=([long]$after.owners[$pair[1].id].rows-[long]$before.owners[$pair[1].id].rows -eq [long]$pair[2])
            $audit.checks["$($pair[0])_versions"]=([long]$after.owners[$pair[1].id].versions-[long]$before.owners[$pair[1].id].versions -eq [long]$pair[3])
        }
        $audit.passed=@($audit.checks.Values|Where-Object{-not $_}).Count -eq 0
        $case.audit=Join-Path $caseDir 'audit.json';Save-Json $audit $case.audit
        $execution=@(Get-ExecutionEvidence $case.started_utc $taskIDs)
        $case.execution_evidence=$execution
        if($point -eq 'both'){
            $overlapMs=$null
            if($execution.Count -eq 2){
                $start=@($execution|ForEach-Object{[DateTimeOffset]::Parse($_.estimated_started_utc)}|Sort-Object|Select-Object -Last 1)[0]
                $end=@($execution|ForEach-Object{[DateTimeOffset]::Parse($_.completed_log_utc)}|Sort-Object|Select-Object -First 1)[0]
                $overlapMs=[Math]::Max(0,($end-$start).TotalMilliseconds)
            }
            $case.overlap=@{two_running_observed_utc=$case.concurrent_running_observed_utc;estimated_worker_interval_overlap_ms=$overlapMs;execution_overlap_confirmed=([bool]$case.concurrent_running_observed_utc -or ($null -ne $overlapMs -and $overlapMs -gt 0));serialized_configuration=($Workers*$Slots -eq 1);note='Observation or successful worker-log intervals only. A single execution slot intentionally serializes the two submitted tasks; no simultaneous execution claim is made. Polling can miss short tasks; queued/start times are not reconstructed.'}
        }
        $case.children=@($inflight|ForEach-Object{Public-Child $_});$case.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
        foreach($service in @('asset-service','iam-service','task-worker')){
            @(& docker @composeArgs logs --no-color --no-log-prefix --since $case.started_utc $service 2>&1|Where-Object{"$_" -match '"msg":"slow database operation"|"msg":"task succeeded"|"msg":"task failed"'}|ForEach-Object{[regex]::Replace([string]$_,'"execution_token"\s*:\s*"[^"]*"','"execution_token":"[REDACTED]"')})|Set-Content (Join-Path $caseDir "$service-events.jsonl")
            if($LASTEXITCODE -ne 0){throw 'Could not preserve case database/worker evidence'}
        }
        $case.status=if(-not $audit.passed){'audit_failed'}elseif($point -eq 'both' -and $Workers*$Slots -gt 1 -and -not $case.overlap.execution_overlap_confirmed){'overlap_unproven'}else{'passed'}
        Save-Json $case (Join-Path $caseDir 'case.json');Save-State
        if(-not $audit.passed){throw 'S7 asset/version reconciliation failed; no subsequent work is permitted'}
        if($point -eq 'both' -and $Workers*$Slots -gt 1 -and -not $case.overlap.execution_overlap_confirmed){throw 'Both tasks succeeded but execution overlap was not established; no claim of simultaneous execution is made'}
    }}
    $state.comparisons=Join-Path $suiteDir 'comparisons.json';Save-Json @(Compare-Cases) $state.comparisons
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
    Write-Output "S7 complete: $stateFile"
}catch{
    $state.status='halted';$state.error=$_.Exception.Message;$state.halted_utc=[DateTimeOffset]::UtcNow.ToString('o')
    Stop-Children
    $state.children=@($children|ForEach-Object{Public-Child $_});Save-State
    Event 'suite_halted' @{reason=$state.error;note='No restore, IAM change, backend cancellation, or new business request is attempted after this failure.'}
    throw
}finally{[Environment]::SetEnvironmentVariable('WORKER_SLOTS',$priorSlots,'Process')}
