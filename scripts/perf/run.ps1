#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$SeedManifest,
    [ValidateSet('read','list','detail','versions','search','deep','mixed','reporting','burst','soak')][string]$Mode='list',
    [double[]]$Rates=@(1,5,10,20,40,80),
    [int]$WarmupSeconds=60,
    [int]$DurationSeconds=180,
    [ValidateRange(1,10)][int]$Repetitions=1,
    [string]$RunPrefix='baseline',
    [string]$OutDir='data/perf/20261015-local-baseline',
    [string]$BaseUrl='http://127.0.0.1:59080/api/v1',
    [string]$EnvFile='.env.perf',
    [ValidateSet('asset-amqp-perf','asset-amqp-opt')][string]$ComposeProject='asset-amqp-perf',
    [string]$RestoreSnapshot='',
    [ValidateSet('none','scoped','search')][string]$IndexProfile='none',
    [string]$AccountScope='',
    [string]$AccountRole='',
    [string]$AccountUsername='',
    [string]$AssetType='',
    [int]$MaxVUs=256,
    [int]$PreAllocatedVUs=0,
    [switch]$Preauthenticate
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
if ($RunPrefix -notmatch '^[a-zA-Z0-9_.-]{1,45}$') { throw 'RunPrefix must be a safe short filename' }
$SeedManifest=(Resolve-Path -LiteralPath $SeedManifest).Path
$null=New-Item -ItemType Directory -Path $OutDir -Force
$OutDir=(Resolve-Path -LiteralPath $OutDir).Path
$minHostFreeMemoryRatio=0.15
$resourcePolicyPath=Join-Path $OutDir 'resource-policy.json'
if(Test-Path -LiteralPath $resourcePolicyPath){
    $policy=Get-Content -LiteralPath $resourcePolicyPath -Raw|ConvertFrom-Json
    $minHostFreeMemoryRatio=[double]$policy.min_host_free_memory_ratio
    if($minHostFreeMemoryRatio -lt 0.01 -or $minHostFreeMemoryRatio -gt 0.5){throw 'Invalid explicit host memory policy'}
}
$allowedRoot=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if (-not $OutDir.StartsWith($allowedRoot,[StringComparison]::OrdinalIgnoreCase)) { throw 'OutDir must be inside ignored data/perf' }
$caseDir=Join-Path $OutDir 'api'
$null=New-Item -ItemType Directory -Path $caseDir -Force
$k6Metadata=Get-Content -Raw -LiteralPath (Join-Path $OutDir 'k6-version.json') | ConvertFrom-Json
if (-not (Test-Path -LiteralPath $k6Metadata.path)) { throw 'Verified k6 executable is unavailable' }
$abortFile=Join-Path $OutDir 'telemetry/abort.json'
$eventsFile=Join-Path $OutDir 'execution.jsonl'
$resultFile=Join-Path $caseDir "$RunPrefix-$Mode-runs.json"
$runs=[Collections.Generic.List[object]]::new()
function Event([string]$Kind,$Details) {
    @{ utc=[DateTimeOffset]::UtcNow.ToString('o'); kind=$Kind; details=$Details } |
        ConvertTo-Json -Depth 12 -Compress | Add-Content -LiteralPath $eventsFile -Encoding utf8
}
function Assert-Resources {
    $os=Get-CimInstance Win32_OperatingSystem
    $ratio=[double]$os.FreePhysicalMemory/[double]$os.TotalVisibleMemorySize
    if ($ratio -lt $minHostFreeMemoryRatio) { throw ('Host free memory {0:P1} is below the configured minimum {1:P1}; no new load was started' -f $ratio,$minHostFreeMemoryRatio) }
    if ((Get-PSDrive -Name D).Free -lt 10GB) { throw 'D drive free space below 10 GiB; no new load was started' }
}
function Read-WriteCounts {
    $values=@{}
    Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
    $owner=''
    if($AccountUsername){
        $seed=Get-Content -Raw -LiteralPath $SeedManifest|ConvertFrom-Json
        $account=@($seed.users|Where-Object username -eq $AccountUsername)
        if($account.Count -ne 1 -or $account[0].id -notmatch '^[0-9a-fA-F-]{36}$'){throw 'Write reconciliation requires a known seed account UUID'}
        $owner=$account[0].id
    }
    $ownerWhere=if($owner){" WHERE owner_id='$owner'"}else{''}
    $versionOwner=if($owner){" AND a.owner_id='$owner'"}else{''}
    $query="SELECT json_build_object('assets',(SELECT count(*) FROM asset.responsible_department_assets$ownerWhere),'versions',(SELECT count(*) FROM asset.asset_versions v JOIN asset.responsible_department_assets a ON a.id::text=v.asset_id::text WHERE v.asset_type='responsible-department'$versionOwner));"
    $raw=& docker compose --env-file $EnvFile -p $ComposeProject exec -T postgres psql -U $values.POSTGRES_USER -d $values.POSTGRES_DB -Atc $query
    if($LASTEXITCODE -ne 0){throw 'Read-only write reconciliation query failed'}
    $counts=$raw|ConvertFrom-Json
    return @{assets=[long]$counts.assets;versions=[long]$counts.versions;owner_id=$owner}
}
Assert-Resources
if (Test-Path -LiteralPath $abortFile) {
    $previousAbort=Get-Content -Raw -LiteralPath $abortFile | ConvertFrom-Json
    # Preserve, rather than erase, the earlier stop condition. Non-resource
    # failures require inspection and are never automatically acknowledged.
    if ($previousAbort.reason -notmatch '^Host free memory|^D drive free space') { throw "Unresolved stop condition: $($previousAbort.reason)" }
    for ($stable=0;$stable -lt 6;$stable++) { Assert-Resources; Start-Sleep -Seconds 5 }
    Move-Item -LiteralPath $abortFile -Destination (Join-Path $OutDir ('telemetry/abort-recovered-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff')+'.json'))
    Event 'resource_recovered' $previousAbort
}
foreach ($rate in $Rates) {
    if ($rate -le 0) { throw 'Rate must be positive' }
    $rateFailed=$false
    for ($repeat=1;$repeat -le $Repetitions;$repeat++) {
        Assert-Resources
        if (Test-Path -LiteralPath $abortFile) { throw 'Collector stop condition prevents further load' }
        $rateLabel=$rate.ToString('0.###',[Globalization.CultureInfo]::InvariantCulture).Replace('.','p')
        $runId="$RunPrefix-$Mode-$rateLabel-r$repeat"
        if ($RestoreSnapshot) {
            & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name $RestoreSnapshot -OutDir $OutDir -EnvFile $EnvFile -ComposeProject $ComposeProject -IndexProfile $IndexProfile *> (Join-Path $caseDir "$runId-restore.log")
            Assert-Resources
        }
        $caseEnv=@{ BASE_URL=$BaseUrl; SEED_MANIFEST=$SeedManifest; MODE=$Mode; RATE=$rate.ToString([Globalization.CultureInfo]::InvariantCulture); WARMUP_SECONDS="$WarmupSeconds"; DURATION_SECONDS="$DurationSeconds"; RUN_ID=$runId; OUT_DIR=$caseDir.Replace('\','/'); MAX_VUS="$MaxVUs"; ACCOUNT_SCOPE=$AccountScope; ACCOUNT_ROLE=$AccountRole; ACCOUNT_USERNAME=$AccountUsername; ASSET_TYPE=$AssetType }
        if($PreAllocatedVUs -gt 0){$caseEnv.PRE_ALLOCATED_VUS="$PreAllocatedVUs"}
        $savedEnv=@{}
        foreach($key in $caseEnv.Keys){$savedEnv[$key]=[Environment]::GetEnvironmentVariable($key,'Process');[Environment]::SetEnvironmentVariable($key,$caseEnv[$key],'Process')}
        $process=$null
        $beforeWrites=$null
        $started=[DateTimeOffset]::UtcNow
        try {
            if($Mode -in @('mixed','burst','soak')){$beforeWrites=Read-WriteCounts}
            Event 'api_started' @{ run_id=$runId; mode=$Mode; rate=$rate; warmup_seconds=$WarmupSeconds; steady_seconds=$DurationSeconds; snapshot=$RestoreSnapshot; index_profile=$IndexProfile; min_host_free_memory_ratio=$minHostFreeMemoryRatio }
            $scriptName=if($Preauthenticate){'api-preauth.js'}else{'api.js'}
            $scriptPath=(Join-Path $PSScriptRoot $scriptName).Replace('\','/')
            $process=Start-Process -FilePath $k6Metadata.path -ArgumentList @('run','--quiet',('"'+$scriptPath+'"')) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $caseDir "$runId.log") -RedirectStandardError (Join-Path $caseDir "$runId.stderr.log")
            while(-not $process.WaitForExit(5000)) {
                if(Test-Path -LiteralPath $abortFile){Stop-Process -Id $process.Id -ErrorAction SilentlyContinue;throw 'Collector requested pressure stop; see telemetry/abort.json'}
            }
            $process.Refresh()
            $summaryPath=Join-Path $caseDir "$runId-summary.json"
            if(-not(Test-Path -LiteralPath $summaryPath)){throw "k6 ended without summary; see $runId.stderr.log"}
            $summary=Get-Content -Raw -LiteralPath $summaryPath|ConvertFrom-Json
            $auditPath=$null
            if($beforeWrites){
                $afterWrites=Read-WriteCounts
                $expectedAssets=[long]$summary.write_effects.fixture_creates+[long]$summary.write_effects.successful_creates_including_warmup
                $expectedVersions=[long]$summary.write_effects.expected_added_versions
                $audit=@{run_id=$runId;owner_id=$beforeWrites.owner_id;before=$beforeWrites;after=$afterWrites;expected_added_assets=$expectedAssets;expected_added_versions=$expectedVersions;observed_added_assets=($afterWrites.assets-$beforeWrites.assets);observed_added_versions=($afterWrites.versions-$beforeWrites.versions);passed=(($afterWrites.assets-$beforeWrites.assets)-eq $expectedAssets -and ($afterWrites.versions-$beforeWrites.versions)-eq $expectedVersions)}
                $auditPath=Join-Path $caseDir "$runId-write-audit.json"
                $audit|ConvertTo-Json -Depth 8|Set-Content -LiteralPath $auditPath -Encoding utf8
                if(-not $audit.passed){throw 'Asset/version write reconciliation failed; no further load is permitted'}
            }
            $record=@{run_id=$runId;started_utc=$started.ToString('o');finished_utc=[DateTimeOffset]::UtcNow.ToString('o');exit_code=$process.ExitCode;summary_path=$summaryPath;load_point_passed=($process.ExitCode -eq 0 -and $summary.load_point_passed);baseline_eligible=($process.ExitCode -eq 0 -and $summary.baseline_eligible);actual_rps=$summary.actual_steady_rps;dropped_iterations=$summary.dropped_iterations}
            if($auditPath){$record.write_audit=$auditPath}
            $runs.Add($record)
            $runs.ToArray()|ConvertTo-Json -Depth 8|Set-Content -LiteralPath $resultFile -Encoding utf8
            Event 'api_finished' $record
            if($process.ExitCode -ne 0 -or -not $summary.load_point_passed){$rateFailed=$true}
            $correctness=@($summary.metrics.PSObject.Properties|Where-Object{$_.Name -like 'correctness_errors*'}|ForEach-Object{$_.Value.values.count}|Where-Object{$_ -gt 0})
            if($correctness.Count){throw "Business correctness failed in $runId; no further load is permitted"}
            Write-Output "$runId actual_rps=$($summary.actual_steady_rps) dropped=$($summary.dropped_iterations) passed=$($summary.load_point_passed)"
        } catch {
            $interruption=@{run_id=$runId;utc=[DateTimeOffset]::UtcNow.ToString('o');error=$_.Exception.Message;baseline_eligible=$false}
            $interruption|ConvertTo-Json -Depth 5|Set-Content -LiteralPath (Join-Path $caseDir "$runId-interrupted.json") -Encoding utf8
            Event 'api_interrupted' $interruption
            throw
        } finally {
            if($process -and -not $process.HasExited){Stop-Process -Id $process.Id -ErrorAction SilentlyContinue}
            foreach($key in $savedEnv.Keys){[Environment]::SetEnvironmentVariable($key,$savedEnv[$key],'Process')}
        }
        if($rateFailed){break}
    }
    if($rateFailed){Event 'rate_escalation_stopped' @{rate=$rate;mode=$Mode};break}
}
Write-Output "API suite complete: $resultFile"
