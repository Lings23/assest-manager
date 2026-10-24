#requires -Version 7.2
# Saturation research retains the unchanged certification verdict at every point.
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/optimization-extreme-20261019',
    [string]$EnvFile='.env.optimization',
    [int[]]$Rates=@(480,640,800,1000,1200,1600),
    [ValidateRange(128,512)][int]$VUs=256,
    [switch]$Resume
)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
# Hold an OS file handle for the entire controller, including final restore.
# A second invocation must fail before touching Compose or starting collectors.
$projectLock=[IO.File]::Open((Join-Path $repo 'data/perf/.asset-amqp-opt-extreme.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
$out=[IO.Path]::GetFullPath((Join-Path $repo $OutDir))
$allowed=[IO.Path]::GetFullPath((Join-Path $repo 'data/perf/optimization-extreme-'))
if(-not $out.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'Use a separate extreme experiment archive'}
$null=New-Item -ItemType Directory -Path $out -Force
$statePath=Join-Path $out 'state.json'
if((Test-Path $statePath) -and -not $Resume){throw 'Existing archive must be preserved; reviewed Resume required'}
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');rates=$Rates;max_vus=$VUs;control_vus=128;short_points=@();refinement=@();certification=@();steps=@{};stop_reason=$null;highest_new_verified_stable_rps=$null;prior_verified_stable_rps=320;error=$null}
$hashes=@{};foreach($file in @('api.js','api-preauth.js','run.ps1','collect.ps1','extreme-throughput.ps1')){$hashes[$file]=(Get-FileHash "$PSScriptRoot/$file").Hash}
if(Test-Path $statePath){
    $state=Get-Content $statePath -Raw|ConvertFrom-Json -AsHashtable
    foreach($file in $hashes.Keys){if($state.script_hashes[$file] -ne $hashes[$file]){throw "Frozen experiment script changed: $file"}}
    if($state.max_vus -ne $VUs -or ($state.rates -join ',') -ne ($Rates -join ',')){throw 'Resume cannot change the protocol'}
    if($state.error){$state.prior_error=$state.error};$state.error=$null
}
$state.script_hashes=$hashes
function Save-State {$state|ConvertTo-Json -Depth 22|Set-Content $statePath}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed; preserved logs contain evidence"}}
function Load([string]$Prefix,[int]$Rate,[int]$Seconds,[int]$Repeats,[int]$Capacity){
    $recordFile="$out/api/$Prefix-mixed-runs.json"
    if(-not(Test-Path $recordFile)){
        $script:environmentTouched=$true
        & "$PSScriptRoot/run.ps1" -Preauthenticate -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -Mode mixed -Rates $Rate -WarmupSeconds 60 -DurationSeconds $Seconds -Repetitions $Repeats -RunPrefix $Prefix -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -RestoreSnapshot d1 -IndexProfile search -MaxVUs $Capacity -PreAllocatedVUs $Capacity *> "$out/$Prefix.log"
    }
    $records=@(Get-Content $recordFile -Raw|ConvertFrom-Json)
    if(-not $records.Count){throw 'No completed point; preserve interrupted results'}
    foreach($record in $records){
        $summary=Get-Content $record.summary_path -Raw|ConvertFrom-Json
        if($summary.preauthentication.sessions -ne 2*$Capacity -or $summary.preauthentication.window_logins -ne 0 -or $summary.preauthentication.window_refreshes -ne 0){throw 'Pure business authentication protocol failed'}
        if(-not (Get-Content $record.write_audit -Raw|ConvertFrom-Json).passed){throw 'SQL assets/versions reconciliation failed'}
        $null=New-Item -ItemType Directory -Path "$out/slow-sql" -Force
        foreach($service in @('gateway','asset-service','iam-service')){
            $since=([DateTimeOffset]$record.started_utc).ToUniversalTime().ToString('o')
            $until=([DateTimeOffset]$record.finished_utc).ToUniversalTime().ToString('o')
            @(& docker logs --since $since --until $until "asset-amqp-opt-$service-1" 2>&1|Where-Object{"$_" -match '"msg":"slow database operation"'})|Set-Content "$out/slow-sql/$($record.run_id)-$service.jsonl"
            Check 'Preserve bounded slow SQL logs'
        }
    }
    return $records
}
function Point($Record){
    $s=Get-Content $Record.summary_path -Raw|ConvertFrom-Json
    $attempts=0L;$success=0L;$worst99=0.0
    foreach($op in $s.operations.PSObject.Properties){$attempts+=$op.Value.attempts;$success+=$op.Value.successes;$worst99=[math]::Max($worst99,[double]$op.Value.duration_ms.'p(99)')}
    return @{target_rps=$s.target_rps;vus=$s.max_vus_per_scenario;attempts=$attempts;successes=$success;successful_rps=$success/$s.steady_seconds;actual_rps=$s.actual_steady_rps;error_rate=if($attempts){1-$success/$attempts}else{1};dropped_iterations=$s.dropped_iterations;worst_endpoint_p99_ms=$worst99;original_gate_passed=[bool]$Record.load_point_passed;record=$Record}
}
Save-State
$collector=$null;$problem=$null;$script:environmentTouched=$false
try {
    $env:OPT_IMAGE_TAG='p2-observe';$env:WORKER_SLOTS='2'
    if(Get-Process -Name k6 -ErrorAction SilentlyContinue){throw 'Another k6 client is active; no competing experiment may start'}
    $old=Get-Content data/perf/optimization-p2/checkpoint-20261018T034834327/containers.jsonl|ForEach-Object{$_|ConvertFrom-Json}
    foreach($expected in $old|Where-Object{$_.name -match 'asset-amqp-opt-(gateway|iam-service|asset-service|governance-service|task-report-service|task-worker|task-publisher)-1$'}){
        $actual=& docker inspect $expected.name.TrimStart('/') --format '{{.Image}}';Check 'Frozen image identity'
        if($actual.Trim() -ne $expected.image_id){throw 'Application images differ from frozen P2 build'}
    }
    foreach($file in @('d1.dump','k6-version.json')){if(-not(Test-Path "$out/$file")){Copy-Item "data/perf/optimization-p2/$file" "$out/$file"}}
    if((Get-FileHash "$out/d1.dump").Hash.ToLowerInvariant() -ne 'f4e53123c4a642140cfe77799ba58046706a524b5c34002cff2acf1fc36d2e13'){throw 'D1 snapshot hash mismatch'}
    @{min_host_free_memory_ratio=0.05;scope='User requested host memory stop line; all original certification gates unchanged'}|ConvertTo-Json|Set-Content "$out/resource-policy.json"
    & node "$PSScriptRoot/preauth-contract-test.mjs" *> "$out/preauth-contract.log";Check 'Complete VU authentication and original business contract'
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -OutDir $out -EnvFile $EnvFile *> "$out/checkpoint-before.log";Check 'Initial metadata'
    if(Test-Path "$out/telemetry/abort.json"){throw 'Unresolved resource/health stop; no automatic continuation'}
    if(Test-Path "$out/telemetry/collector.stop"){Move-Item "$out/telemetry/collector.stop" "$out/telemetry/prior-stop-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))"}
    $arguments=@('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio','0.05')
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput "$out/collector.log" -RedirectStandardError "$out/collector.stderr.log"
    $state.collector_pid=$collector.Id;Save-State
    if(-not $state.steps.control){
        $state.status='480-rps-128-vu-control';Save-State
        $records=@(Load 'extreme-control128' 480 180 1 128)
        $state.control=Point $records[0];$state.steps.control=$true;Save-State
    }
    $state.status='saturation-staircase';Save-State
    foreach($rate in $state.rates){
        if($state.stop_reason){break}
        if(@($state.short_points|Where-Object target_rps -eq $rate).Count){continue}
        $state.active_target=$rate;Save-State
        $records=@(Load "extreme-step-$rate" $rate 180 1 $VUs)
        $point=Point $records[0];$state.short_points+=@($point);Save-State
        # Research can continue after an SLA/drop failure, but these separate
        # research safety criteria never convert that failed point to a pass.
        if($point.error_rate -ge 0.01){$state.stop_reason='Unexpected business errors reached 1%; no further escalation'}
        elseif($point.worst_endpoint_p99_ms -ge 5000){$state.stop_reason='An endpoint p99 reached 5 seconds; bounded queue safety stop'}
        elseif($state.short_points.Count -ge 3){
            $last=@($state.short_points|Select-Object -Last 3)
            $g1=$last[1].successful_rps/$last[0].successful_rps-1
            $g2=$last[2].successful_rps/$last[1].successful_rps-1
            if($g1 -lt 0.05 -and $g2 -lt 0.05 -and $point.actual_rps -lt 0.95*$rate){$state.stop_reason='Two increasing offers gained less than 5% successful throughput with arrival loss; observed plateau'}
        }
        Save-State
    }
    if(-not $state.stop_reason){$state.stop_reason='Completed bounded candidate matrix; no unbounded escalation'}
    # Refine the first original-gate failure, separately from saturated points.
    $state.status='stable-boundary-refinement';Save-State
    for($i=0;$i -lt 2;$i++){
        $all=@($state.short_points)+@($state.refinement)
        $failed=@($all|Where-Object{-not $_.original_gate_passed}|Sort-Object target_rps)
        if(-not $failed.Count){break}
        $upper=[int]$failed[0].target_rps
        $lower=320
        foreach($p in $all|Where-Object{$_.original_gate_passed -and $_.target_rps -lt $upper}){$lower=[math]::Max($lower,[int]$p.target_rps)}
        if($upper-$lower -le 40){break}
        $candidate=[int](20*[math]::Floor((($upper+$lower)/2)/20))
        if($candidate -le $lower -or $candidate -ge $upper){break}
        $state.active_target=$candidate;Save-State
        $records=@(Load "extreme-refine-$candidate" $candidate 180 1 $VUs)
        $state.refinement+=@(Point $records[0]);Save-State
    }
    $state.status='certification';Save-State
    $candidates=@(@($state.short_points)+@($state.refinement)|Where-Object original_gate_passed|ForEach-Object{[int]$_.target_rps}|Sort-Object -Descending -Unique)
    foreach($candidate in $candidates){
        $existing=@($state.certification|Where-Object rps -eq $candidate)
        if($existing.Count){if($existing[0].passed){break}else{continue}}
        $state.active_target=$candidate;Save-State
        $records=@(Load "extreme-repeat-$candidate" $candidate 600 3 $VUs)
        $passed=($records.Count -eq 3 -and @($records|Where-Object{-not $_.load_point_passed -or -not $_.baseline_eligible -or $_.dropped_iterations -ne 0}).Count -eq 0)
        $state.certification+=@(@{rps=$candidate;passed=$passed;records=$records});Save-State
        if($passed){$state.highest_new_verified_stable_rps=$candidate;break}
    }
    $state.status='pressure-completed';Save-State
} catch {$problem=$_;$state.status='interrupted';$state.error=$_.Exception.Message;Save-State}
finally {
    if($collector){'Stop after experiment'|Set-Content "$out/telemetry/collector.stop";Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue}
    try {
        if($script:environmentTouched -and (Test-Path "$out/d1.dump")){
            & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers 1 *> "$out/final-restore.log";Check 'Final D1 restore'
            & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -OutDir $out -EnvFile $EnvFile *> "$out/checkpoint-final.log";Check 'Final metadata'
            $state.steps.final_restore=$true
        }
    } catch {$state.restore_error=$_.Exception.Message;if(-not $problem){$problem=$_}}
    if(-not $problem){$state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')}
    Save-State
    Remove-Item Env:OPT_IMAGE_TAG,Env:WORKER_SLOTS -ErrorAction SilentlyContinue
    $projectLock.Dispose()
}
if($problem){throw $problem}
