#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$EnvFile='.env.optimization',
    [ValidatePattern('^p2[a-z0-9-]*$')][string]$ImageTag='p2-observe',
    [ValidateSet('scoped','search')][string]$IndexProfile='search',
    [ValidatePattern('^[a-zA-Z0-9_.-]{1,24}$')][string]$RunPrefix='p2-capacity',
    [ValidateRange(16,2048)][int]$MaxVUs=128,
    [ValidateRange(4,2048)][int]$PreAllocatedVUs=128,
    [switch]$Resume
)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p2'
$statePath=Join-Path $out "$RunPrefix-state.json"
if((Test-Path -LiteralPath $statePath) -and -not $Resume){throw 'Capacity archive exists; inspect it before explicit Resume'}
if($PreAllocatedVUs -gt $MaxVUs){throw 'Preallocated VUs exceed maximum'}
$readyPath=Join-Path $out 'implementation-ready.json'
if(-not(Test-Path -LiteralPath $readyPath)){throw 'Build, regression and monitoring overhead verification must precede the capacity matrix'}
$ready=Get-Content -LiteralPath $readyPath -Raw|ConvertFrom-Json
if(-not $ready.functional_passed -or -not $ready.metrics_verified -or -not $ready.overhead_pair_completed -or $ready.image_tag -ne $ImageTag){throw 'Current image is not the verified P2 implementation'}
$manifest=Join-Path $repo 'data/perf/20261016-local-baseline/seed-d1/seed-manifest.json'
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');rates=@(80,120,160,240,320,480,640,800);image_tag=$ImageTag;index_profile=$IndexProfile;max_vus=$MaxVUs;preallocated_vus=$PreAllocatedVUs;staircase=@();repeats=@();first_failed_target=$null;highest_passing_short_point=$null;highest_verified_stable_rps=$null;production_capacity_claim=$false;error=$null}
$state.script_hashes=@{}
foreach($file in @('api.js','run.ps1','collect.ps1','optimization-capacity.ps1')){$state.script_hashes[$file]=(Get-FileHash (Join-Path $PSScriptRoot $file) -Algorithm SHA256).Hash}
if(Test-Path -LiteralPath $statePath){
    $previous=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json -AsHashtable
    if($previous.image_tag -ne $ImageTag -or $previous.index_profile -ne $IndexProfile -or $previous.max_vus -ne $MaxVUs -or $previous.preallocated_vus -ne $PreAllocatedVUs){throw 'Do not reuse results under changed image/index/VU configuration'}
    foreach($file in @('api.js','run.ps1','collect.ps1')){if($previous.script_hashes[$file] -ne $state.script_hashes[$file]){throw "Benchmark/collector script changed: $file"}}
    $revisions=if($previous.ContainsKey('controller_revisions')){@($previous.controller_revisions)}else{@()}
    $previous.controller_revisions=@($revisions)+@(@{utc=[DateTimeOffset]::UtcNow.ToString('o');sha256=$state.script_hashes['optimization-capacity.ps1'];previous_error=$previous.error})
    $state=$previous;$state.error=$null
}
function Save-State {$state|ConvertTo-Json -Depth 16|Set-Content -LiteralPath $statePath}
function Run-Load([string]$Prefix,[int]$Rate,[int]$Seconds,[int]$Repeats){
    $recordFile=Join-Path $out "api/$Prefix-mixed-runs.json"
    if(-not(Test-Path -LiteralPath $recordFile)){
        & (Join-Path $PSScriptRoot 'run.ps1') -SeedManifest $manifest -Mode mixed -Rates $Rate -WarmupSeconds 60 -DurationSeconds $Seconds -Repetitions $Repeats -RunPrefix $Prefix -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -RestoreSnapshot d1 -IndexProfile $IndexProfile -MaxVUs $MaxVUs -PreAllocatedVUs $PreAllocatedVUs *> (Join-Path $out "$Prefix.log")
    }
    $records=@(Get-Content -LiteralPath $recordFile -Raw|ConvertFrom-Json)
    if(-not $records.Count){throw 'Missing final load orchestration records'}
    if($records.Count -lt $Repeats -and $records[-1].load_point_passed){throw 'Incomplete passed repetition group; preserve it and use a new reviewed prefix'}
    $null=New-Item -ItemType Directory -Path (Join-Path $out 'slow-sql') -Force
    foreach($record in $records){
        foreach($service in @('gateway','asset-service','iam-service','task-report-service','task-worker','task-publisher')){
            $since=([DateTimeOffset]$record.started_utc).ToUniversalTime().ToString('o')
            $until=([DateTimeOffset]$record.finished_utc).ToUniversalTime().ToString('o')
            $slow=@(& docker logs --since $since --until $until "asset-amqp-opt-$service-1" 2>&1 | Where-Object{"$_" -match '"msg":"slow database operation"'})
            if($LASTEXITCODE -ne 0){throw "Could not preserve slow-query observations for $service"}
            $slow|Set-Content -LiteralPath (Join-Path $out "slow-sql/$($record.run_id)-$service.jsonl")
        }
    }
    return $records
}
Save-State
try {
    $env:OPT_IMAGE_TAG=$ImageTag
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p2 -EnvFile $EnvFile *> (Join-Path $out "$RunPrefix-checkpoint-before.log")
    $state.status='staircase';Save-State
    foreach($rate in $state.rates){
        $existing=@($state.staircase|Where-Object{$_.target_rps -eq $rate})
        if($existing.Count){if(-not $existing[0].record.load_point_passed){break}else{continue}}
        $record=@(Run-Load "$RunPrefix-step-$rate" $rate 180 1)
        if($record.Count -ne 1){throw 'A short staircase point must have exactly one final record'}
        $state.staircase+=@{target_rps=$rate;record=$record[0]};Save-State
        if(-not $record[0].load_point_passed){$state.first_failed_target=$rate;break}
        $state.highest_passing_short_point=$rate;Save-State
    }
    if($null -eq $state.highest_passing_short_point){throw 'No short point passed; diagnose before sustained repetitions'}
    $state.status='candidate_repeats';Save-State
    $state.candidate_attempts=@()
    $passed=$false
    # A short pass is only a candidate. Preserve failed sustained candidates and
    # certify a lower already-passing point if necessary; never raise load again.
    foreach($candidate in @($state.staircase|Where-Object{$_.record.load_point_passed}|ForEach-Object{[int]$_.target_rps}|Sort-Object -Descending)){
        $state.current_candidate=$candidate;Save-State
        $repeats=@(Run-Load "$RunPrefix-repeat-$candidate" $candidate 600 3)
        $state.repeats=$repeats
        $passed=($repeats.Count -eq 3 -and @($repeats|Where-Object{-not $_.load_point_passed -or -not $_.baseline_eligible -or $_.dropped_iterations -ne 0 -or $_.exit_code -ne 0}).Count -eq 0)
        $result=@{rps=$candidate;all_repeats_passed=$passed;three_repeats_completed=$repeats.Count;source_runs=$repeats;scope='Frozen D1, original mixed ratio and permission account pool on this shared local host';production_capacity_claim=$false;image_tag=$ImageTag;index_profile=$IndexProfile;max_vus=$MaxVUs;preallocated_vus=$PreAllocatedVUs;first_failed_target=$state.first_failed_target;upper_bound_found=$false;note='A tested RPS is not a maximum. Failed candidates remain archived; no renewed escalation after a failed point.'}
        $result|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $out "$RunPrefix-candidate-$candidate.json")
        $state.candidate_attempts+=$result;Save-State
        if($passed){$state.highest_verified_stable_rps=$candidate;break}
        if(Test-Path (Join-Path $out 'telemetry/abort.json')){throw 'Resource/health stop prevents lower candidate tests'}
    }
    if(-not $passed){$state.status='candidate_not_verified';Save-State;return}
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p2 -EnvFile $EnvFile *> (Join-Path $out "$RunPrefix-checkpoint-final.log")
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
