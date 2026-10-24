#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[string]$OutDir='data/perf/optimization-p2/preauthenticated-20261018',[switch]$Resume,[switch]$AcceptResourcePolicyChange)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=[IO.Path]::GetFullPath((Join-Path $repo $OutDir))
$allowed=[IO.Path]::GetFullPath((Join-Path $repo 'data/perf/optimization-p2/preauthenticated-'))
if(-not $out.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'New protocol must use a separate P2 preauthenticated archive'}
$null=New-Item -ItemType Directory -Path $out -Force
$statePath=Join-Path $out 'state.json'
if((Test-Path $statePath) -and -not $Resume){throw 'Preserve existing experiment; use an explicitly reviewed Resume'}
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');rates=@(240,320,480,640,800);max_vus=128;preallocated_vus=128;image_tag='p2-observe';index_profile='search';authentication_protocol='independent-per-VU-setup';short_points=@();certification=@();highest_verified_stable_rps=$null;first_failed_target=$null;steps=@{};error=$null}
$hashes=@{};foreach($file in @('api.js','api-preauth.js','run.ps1','collect.ps1','optimization-preauth.ps1')){$hashes[$file]=(Get-FileHash "$PSScriptRoot/$file").Hash}
if(Test-Path $statePath){
    $state=Get-Content $statePath -Raw|ConvertFrom-Json -AsHashtable
    foreach($file in @('api.js','api-preauth.js')){if($state.script_hashes[$file] -ne $hashes[$file]){throw "Frozen business protocol changed: $file"}}
    foreach($file in @('run.ps1','collect.ps1')){
        if($state.script_hashes[$file] -ne $hashes[$file]){
            if(-not $AcceptResourcePolicyChange -or -not(Test-Path "$out/resource-policy.json")){throw "Frozen runner/collector changed; explicit reviewed resource-policy revision required: $file"}
            $prior=if($state.ContainsKey('resource_policy_revisions')){@($state.resource_policy_revisions)}else{@()}
            $state.resource_policy_revisions=@($prior)+@(@{file=$file;old_hash=$state.script_hashes[$file];new_hash=$hashes[$file];utc=[DateTimeOffset]::UtcNow.ToString('o');reason='Explicit user-requested host memory policy change; benchmark API frozen'})
        }
    }
    if($state.error){$state.prior_error=$state.error};$state.error=$null
}
$state.script_hashes=$hashes
function Save-State {$state|ConvertTo-Json -Depth 18|Set-Content $statePath}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed; inspect preserved logs"}}
function Load([string]$Prefix,[int]$Rate,[int]$Seconds,[int]$Repeats,[int]$VUs=128,[int]$Warmup=60){
    $recordFile="$out/api/$Prefix-mixed-runs.json"
    if(-not(Test-Path $recordFile)){
        & "$PSScriptRoot/run.ps1" -Preauthenticate -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -Mode mixed -Rates $Rate -WarmupSeconds $Warmup -DurationSeconds $Seconds -Repetitions $Repeats -RunPrefix $Prefix -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -RestoreSnapshot d1 -IndexProfile search -MaxVUs $VUs -PreAllocatedVUs $VUs *> "$out/$Prefix.log"
        Check 'Preauthenticated load runner'
    }
    $records=@(Get-Content $recordFile -Raw|ConvertFrom-Json)
    if(-not $records.Count -or ($records.Count -lt $Repeats -and $records[-1].load_point_passed)){throw 'Incomplete passed group; preserve it and choose a new archive'}
    foreach($record in $records){
        $summary=Get-Content $record.summary_path -Raw|ConvertFrom-Json
        if($summary.preauthentication.sessions -ne 2*$VUs -or $summary.preauthentication.window_logins -ne 0 -or $summary.preauthentication.window_refreshes -ne 0){throw 'Pure business protocol not established; retain all samples, do not certify'}
        if(-not (Get-Content $record.write_audit -Raw|ConvertFrom-Json).passed){throw 'SQL write audit failed'}
        $null=New-Item -ItemType Directory -Path "$out/slow-sql" -Force
        foreach($service in @('gateway','asset-service','iam-service')){
            $since=([DateTimeOffset]$record.started_utc).ToUniversalTime().ToString('o');$until=([DateTimeOffset]$record.finished_utc).ToUniversalTime().ToString('o')
            @(& docker logs --since $since --until $until "asset-amqp-opt-$service-1" 2>&1|Where-Object{"$_" -match '"msg":"slow database operation"'})|Set-Content "$out/slow-sql/$($record.run_id)-$service.jsonl"
            Check 'Preserve slow SQL'
        }
    }
    return $records
}
Save-State
try {
    $env:OPT_IMAGE_TAG='p2-observe'
    $old=Get-Content data/perf/optimization-p2/checkpoint-20261018T034834327/containers.jsonl|ForEach-Object{$_|ConvertFrom-Json}
    foreach($expected in $old|Where-Object{$_.name -match 'asset-amqp-opt-(gateway|iam-service|asset-service|governance-service|task-report-service|task-worker|task-publisher)-1$'}){
        $actual=& docker inspect $expected.name.TrimStart('/') --format '{{.Image}}';Check 'Current image identity'
        if($actual.Trim() -ne $expected.image_id){throw 'Running application image differs from frozen P2 image; no silent deployment change'}
    }
    foreach($file in @('d1.dump','k6-version.json')){if(-not(Test-Path "$out/$file")){Copy-Item "data/perf/optimization-p2/$file" "$out/$file"}}
    & node "$PSScriptRoot/preauth-contract-test.mjs" *> "$out/preauth-contract.log";Check 'Session isolation contract tests'
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -EnvFile $EnvFile *> "$out/checkpoint-before.log";Check 'Before checkpoint'
    $stop="$out/telemetry/collector.stop"
    if(Test-Path $stop){Move-Item -LiteralPath $stop -Destination "$out/telemetry/prior-stop-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))"}
    $minimum=0.15
    if(Test-Path "$out/resource-policy.json"){$minimum=(Get-Content "$out/resource-policy.json" -Raw|ConvertFrom-Json).min_host_free_memory_ratio}
    $arguments=@('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio',"$minimum")
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru
    $state.collector_pid=$collector.Id;Save-State
    if(-not $state.steps.smoke){
        $state.status='smoke';Save-State
        $smoke=@(Load 'preauth-smoke-v2' 4 30 1 4 10)
        if(-not $smoke[0].load_point_passed){throw 'Preauthentication real k6 smoke failed'}
        $state.steps.smoke=$true;Save-State
    }
    $state.status='staircase';Save-State
    foreach($rate in $state.rates){
        $existing=@($state.short_points|Where-Object target_rps -eq $rate)
        if($existing.Count){if(-not $existing[0].record.load_point_passed){break}else{continue}}
        $records=@(Load "preauth-step-$rate" $rate 180 1)
        $state.short_points+=@{target_rps=$rate;record=$records[0]};Save-State
        if(-not $records[0].load_point_passed){$state.first_failed_target=$rate;Save-State;break}
    }
    $state.status='certification';Save-State
    foreach($candidate in @($state.short_points|Where-Object{$_.record.load_point_passed}|ForEach-Object{[int]$_.target_rps}|Sort-Object -Descending)){
        $records=@(Load "preauth-repeat-$candidate" $candidate 600 3)
        $passed=($records.Count -eq 3 -and @($records|Where-Object{-not $_.load_point_passed -or -not $_.baseline_eligible -or $_.dropped_iterations -ne 0}).Count -eq 0)
        $state.certification+=@{rps=$candidate;passed=$passed;records=$records};Save-State
        if($passed){$state.highest_verified_stable_rps=$candidate;break}
        if(Test-Path "$out/telemetry/abort.json"){throw 'Resource/health stop prevents further testing'}
    }
    'Completed pressure'|Set-Content $stop
    Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue
    & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> "$out/final-restore.log";Check 'Final D1 restore'
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -EnvFile $EnvFile *> "$out/checkpoint-final.log";Check 'Final checkpoint'
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
} catch {$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally {
    if($collector){'Stopped by experiment finalizer'|Set-Content "$out/telemetry/collector.stop";Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue}
    Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue
}
