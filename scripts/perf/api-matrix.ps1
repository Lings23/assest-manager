#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/20261016-local-baseline',
    [string]$EnvFile='.env.perf',
    [ValidateSet('All','Remaining','Prepare','Reads','Queries','Reporting','Mixed','Burst','Soak')][string]$Stage='All',
    [string]$RunPrefix='api-20261016'
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$OutDir=[IO.Path]::GetFullPath($OutDir)
$allowed=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $OutDir.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'OutDir must be inside ignored data/perf'}
if($RunPrefix -notmatch '^[a-zA-Z0-9_-]{1,24}$'){throw 'RunPrefix must be a short safe identifier'}
$seedDir=Join-Path $OutDir 'seed-d1'
$manifest=Join-Path $seedDir 'seed-manifest.json'
$statusPath=Join-Path $OutDir "$RunPrefix-$Stage-status.json"
$eventsPath=Join-Path $OutDir 'api-matrix-events.jsonl'
$state=[ordered]@{stage=$Stage;status='running';started_utc=[DateTimeOffset]::UtcNow.ToString('o');current=$null;completed=@();candidate_rps=$null;error=$null}
$state.script_sha256=@{}
foreach($scriptName in @('api.js','run.ps1','seed.ps1','database.ps1','api-matrix.ps1')){$state.script_sha256[$scriptName]=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot $scriptName) -Algorithm SHA256).Hash}
function Save-State { $state|ConvertTo-Json -Depth 16|Set-Content -LiteralPath $statusPath -Encoding utf8 }
function Record-Event([string]$Kind,$Details){@{utc=[DateTimeOffset]::UtcNow.ToString('o');kind=$Kind;details=$Details}|ConvertTo-Json -Depth 12 -Compress|Add-Content -LiteralPath $eventsPath -Encoding utf8}
function Assert-Resources {
    if(Test-Path -LiteralPath (Join-Path $OutDir 'telemetry/abort.json')){throw 'Collector stop condition prevents further API load'}
    $os=Get-CimInstance Win32_OperatingSystem
    if(([double]$os.FreePhysicalMemory/$os.TotalVisibleMemorySize)-lt 0.15){throw 'Host free memory is below the plan minimum 15%'}
    if((Get-PSDrive -Name D).Free-lt 10GB){throw 'D drive free space below 10 GiB'}
}
function Invoke-Checkpoint([string]$Mode,[string]$Name) {
    Assert-Resources
    $log=Join-Path $OutDir "$RunPrefix-$Mode-$Name.log"
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode $Mode -Name $Name -OutDir $OutDir -EnvFile $EnvFile *> $log
    if(-not $?){throw "Checkpoint $Mode failed: $Name"}
}
function Invoke-ApiCase([string]$Name,[string]$Mode,[double[]]$Rates,[int]$Seconds=180,[int]$Repeat=1,[string]$Scope='',[string]$Username='',[string]$Snapshot='',[string]$AssetType='') {
    Assert-Resources
    $prefix="$RunPrefix-$Name"
    $state.current=@{name=$Name;mode=$Mode;rates=$Rates;steady_seconds=$Seconds;repeat=$Repeat;snapshot=$Snapshot};Save-State
    Record-Event 'case_started' $state.current
    $caseArguments=@{SeedManifest=$manifest;Mode=$Mode;Rates=$Rates;WarmupSeconds=60;DurationSeconds=$Seconds;Repetitions=$Repeat;RunPrefix=$prefix;OutDir=$OutDir;EnvFile=$EnvFile;AccountScope=$Scope;AccountUsername=$Username;AssetType=$AssetType;MaxVUs=128;PreAllocatedVUs=128}
    if($Snapshot){$caseArguments.RestoreSnapshot=$Snapshot}
    $log=Join-Path $OutDir "$prefix.log"
    & (Join-Path $PSScriptRoot 'run.ps1') @caseArguments *> $log
    if(-not $?){throw "API case interrupted: $Name; see $log"}
    $resultPath=Join-Path $OutDir "api/$prefix-$Mode-runs.json"
    $runs=@(Get-Content -Raw -LiteralPath $resultPath|ConvertFrom-Json)
    $state.completed+=@{name=$Name;mode=$Mode;runs_file=$resultPath;passed_points=@($runs|Where-Object load_point_passed).Count;failed_points=@($runs|Where-Object{-not $_.load_point_passed}).Count}
    Save-State;Record-Event 'case_finished' $state.completed[-1]
    Write-Host "API matrix case complete: $Name"
    return $runs
}
function Get-Candidate {
    $path=Join-Path $OutDir "$RunPrefix-candidate.json"
    if(-not(Test-Path -LiteralPath $path)){throw 'Mixed staircase must run before Burst or Soak'}
    return Get-Content -Raw -LiteralPath $path|ConvertFrom-Json
}
Save-State
try {
    Assert-Resources
    if($Stage -in @('All','Prepare')) {
        if(Test-Path -LiteralPath $manifest){
            $previous=Get-Content -Raw -LiteralPath $manifest|ConvertFrom-Json
            if($previous.state -ne 'completed'){throw 'Incomplete D1 seed exists; inspect it before reseeding'}
            if(-not(Test-Path -LiteralPath (Join-Path $OutDir 'd1.dump'))){throw 'D1 manifest has no matching durable checkpoint'}
        } else {
            $state.current=@{name='prepare-d1';rows=100000};Save-State
            Invoke-Checkpoint restore empty
            & (Join-Path $PSScriptRoot 'seed.ps1') -Rows 100000 -Seed 20261015 -EnvFile $EnvFile -OutDir $seedDir *> (Join-Path $OutDir "$RunPrefix-seed-d1.log")
            if(-not $?){throw 'D1 fixture preparation failed'}
            Invoke-Checkpoint snapshot d1
        }
        $state.completed+=@{name='prepare-d1';rows=100000;manifest=$manifest};Save-State
    }
    if(-not(Test-Path -LiteralPath $manifest)){throw 'Prepare D1 before API scenarios'}
    if($Stage -in @('All','Reads')) {
        Invoke-Checkpoint restore d1
        foreach($mode in @('list','detail','versions')) {
            $null=Invoke-ApiCase "s1-$mode" $mode @(1,5,10,20,40,80)
        }
    }
    if($Stage -in @('All','Queries')) {
        Invoke-Checkpoint restore d1
        # Keep every search selectivity/pagination point in a separate result.
        $seed=Get-Content -Raw -LiteralPath $manifest|ConvertFrom-Json
        $admin=(Get-Content -Raw -LiteralPath $seed.credentials_file|ConvertFrom-Json).admin
        $savedEnv=@{}
        foreach($key in @('LIMIT','OFFSET','SEARCH_QUERY','SEARCH_MATCH','SEARCH_EXPECT_MIN','SEARCH_EXPECT_MAX')){$savedEnv[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
        try {
            foreach($limit in @(20,100)) {
                $env:LIMIT="$limit"
                $count=[int]$seed.per_user_counts.($admin.id).'responsible-department'
                foreach($offset in @(0,1000,10000,([Math]::Max(0,[Math]::Floor(($count-1)/$limit)*$limit)))) {
                    $env:OFFSET="$offset"
                    $null=Invoke-ApiCase "s2-l$limit-o$offset" 'deep' @(20) 180 1 '' $admin.username '' 'responsible-department'
                }
            }
            [Environment]::SetEnvironmentVariable('OFFSET',$null,'Process');$env:LIMIT='20'
            foreach($case in $seed.search_cases) {
                $env:SEARCH_QUERY=$case.query;$env:SEARCH_MATCH=$case.label;$env:SEARCH_EXPECT_MIN="$($case.expected_min)";$env:SEARCH_EXPECT_MAX="$($case.expected_max)"
                $null=Invoke-ApiCase "s2-search-$($case.label)" 'search' @(20) 180 1 '' $admin.username '' 'responsible-department'
            }
        } finally {foreach($key in $savedEnv.Keys){[Environment]::SetEnvironmentVariable($key,$savedEnv[$key],'Process')}}
    }
    if($Stage -in @('All','Remaining','Reporting')) {
        Invoke-Checkpoint restore d1
        foreach($scope in @('global','department')){$null=Invoke-ApiCase "s4-$scope" 'reporting' @(0.1,0.2,0.5,1) 180 1 $scope}
    }
    if($Stage -in @('All','Remaining','Mixed')) {
        $mixedRuns=Invoke-ApiCase 's3-stairs' 'mixed' @(1,5,10,20,40,80) 180 1 '' '' d1
        $candidate=@($mixedRuns|Where-Object load_point_passed|Sort-Object { $summary=Get-Content -Raw -LiteralPath $_.summary_path|ConvertFrom-Json;$summary.target_rps }|Select-Object -Last 1)
        if($candidate.Count -ne 1){throw 'No passing mixed load point exists'}
        $summary=Get-Content -Raw -LiteralPath $candidate[0].summary_path|ConvertFrom-Json
        $rate=[double]$summary.target_rps;$state.candidate_rps=$rate;Save-State
        $repeatSeconds=[Math]::Max(600,[Math]::Ceiling(100/($rate*0.05)))
        $repeats=Invoke-ApiCase 's3-repeat' 'mixed' @($rate) $repeatSeconds 3 '' '' d1
        $candidateResult=@{rps=$rate;three_repeats_completed=$repeats.Count;all_repeats_passed=(@($repeats|Where-Object{-not $_.load_point_passed -or -not $_.baseline_eligible}).Count-eq 0 -and $repeats.Count-eq 3);scope='mixed D1 workload on this host';source_runs=$repeats;production_capacity_claim=$false}
        $candidateResult|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $OutDir "$RunPrefix-candidate.json") -Encoding utf8
        if(-not $candidateResult.all_repeats_passed){throw 'Mixed candidate did not pass three comparable repetitions; stop dependent burst/soak'}
    }
    if($Stage -in @('All','Remaining','Burst')) {
        $candidate=Get-Candidate
        if(-not $candidate.all_repeats_passed){throw 'Burst requires a passing repeated reference point'}
        $null=Invoke-ApiCase 's9-burst' 'burst' @([double]$candidate.rps*0.6) 300 1 '' '' d1
    }
    if($Stage -in @('All','Remaining','Soak')) {
        $candidate=Get-Candidate
        if(-not $candidate.all_repeats_passed){throw 'Soak requires a passing repeated reference point'}
        $null=Invoke-ApiCase 's9-soak' 'soak' @([double]$candidate.rps*0.6) 3600 1 '' '' d1
    }
    $state.current=$null;$state.status='completed'
} catch {$state.status='interrupted';$state.error=$_.Exception.Message;Record-Event 'matrix_interrupted' @{error=$state.error;current=$state.current};throw}
finally {$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State}
Write-Output "API matrix complete: $statusPath"
