#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/20261016-local-baseline',
    [string]$EnvFile='.env.perf',
    [string]$ApiPrefix='api-20261017',
    [ValidateSet('All','Remaining')][string]$ApiStage='All',
    [string]$InterferencePrefix='s7-20261017',
    [string]$CorrectnessRunId='s10-20261017',
    [ValidateRange(1,3)][int]$Repetitions=3
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$OutDir=[IO.Path]::GetFullPath($OutDir)
$allowed=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $OutDir.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'OutDir must be inside ignored data/perf'}
foreach($value in @($ApiPrefix,$InterferencePrefix,$CorrectnessRunId)){if($value -notmatch '^[a-zA-Z0-9_-]{1,30}$'){throw 'Invalid run identifier'}}
$state=@{status='waiting_for_api';started_utc=[DateTimeOffset]::UtcNow.ToString('o');interference_repetitions=$Repetitions;error=$null;script_sha256=@{}}
foreach($name in @('after-api.ps1','interference.ps1','jobs.ps1','api.js','run.ps1','database.ps1')){$state.script_sha256[$name]=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot $name)).Hash}
$statusPath=Join-Path $OutDir 'dependent-scenarios-status.json'
function Save-State {$state|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $statusPath -Encoding utf8}
Save-State
try{
    while($true){
        if(Test-Path -LiteralPath (Join-Path $OutDir 'telemetry/abort.json')){throw 'Resource stop prevents dependent scenarios'}
        if(Test-Path -LiteralPath (Join-Path $OutDir 'dependent-scenarios.stop')){throw 'Explicit dependent-scenario stop marker'}
        try{$api=Get-Content -LiteralPath (Join-Path $OutDir "$ApiPrefix-$ApiStage-status.json") -Raw|ConvertFrom-Json}catch{Start-Sleep -Seconds 5;continue}
        if($api.status -eq 'interrupted'){throw 'API matrix interrupted; inspect before dependent scenarios'}
        if($api.status -eq 'completed'){break}
        Start-Sleep -Seconds 5
    }
    $candidatePath=Join-Path $OutDir "$ApiPrefix-candidate.json"
    $candidate=Get-Content -LiteralPath $candidatePath -Raw|ConvertFrom-Json
    if(-not $candidate.all_repeats_passed -or $candidate.three_repeats_completed -ne 3){throw 'No validated repeated mixed reference point'}
    foreach($name in @('burst','soak')){
        $runs=@(Get-Content -LiteralPath (Join-Path $OutDir "api/$ApiPrefix-s9-$name-$name-runs.json") -Raw|ConvertFrom-Json)
        if($runs.Count -ne 1 -or -not $runs[0].load_point_passed -or $runs[0].exit_code -ne 0){throw "S9 $name requires review"}
        $audit=Get-Content -LiteralPath $runs[0].write_audit -Raw|ConvertFrom-Json
        if(-not $audit.passed){throw 'S9 asset/version audit failed'}
    }
    # At 0.1 RPS, one boundary arrival in 180 seconds changes the rate by
    # 5.56%. Use 240 seconds for this separate measurement; keep the 5% gate.
    $state.status='running_reporting_remeasurement';Save-State
    & (Join-Path $PSScriptRoot 'run.ps1') -SeedManifest (Join-Path $OutDir 'seed-d1/seed-manifest.json') -Mode reporting -Rates @(0.1,0.2,0.5,1) -WarmupSeconds 60 -DurationSeconds 240 -Repetitions 1 -RunPrefix 'stats-dept240-20261017' -OutDir $OutDir -EnvFile $EnvFile -AccountScope department -RestoreSnapshot d1 -MaxVUs 128 -PreAllocatedVUs 128 *> (Join-Path $OutDir 'stats-dept240-20261017.log')
    if(-not $?){throw 'Reporting remeasurement interrupted'}
    $state.reporting_remeasurement_runs=Join-Path $OutDir 'api/stats-dept240-20261017-reporting-runs.json';Save-State
    $state.status='running_interference';Save-State
    & (Join-Path $PSScriptRoot 'interference.ps1') -OutDir $OutDir -CandidatePath $candidatePath -EnvFile $EnvFile -RunPrefix $InterferencePrefix -Repetitions $Repetitions *> (Join-Path $OutDir 'interference-20261017.log')
    if(-not $?){throw 'Interference suite interrupted'}
    $state.status='running_correctness';Save-State
    & (Join-Path $PSScriptRoot 'jobs.ps1') -Mode correctness -EnvFile $EnvFile -OutDir (Join-Path $OutDir "jobs/$CorrectnessRunId") -RunId $CorrectnessRunId -Repetitions 1 *> (Join-Path $OutDir 's10-20261017.log')
    if(-not $?){throw 'Concurrent correctness suite interrupted'}
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
    Write-Output "Dependent scenarios complete: $statusPath"
}catch{$state.status='halted';$state.error=$_.Exception.Message;$state.halted_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State;throw}
