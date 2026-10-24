#requires -Version 7.2
# Separate generator-capacity diagnostic, never a replacement certification.
param([string]$OutDir='data/perf/optimization-extreme-20261019-client512',[string]$EnvFile='.env.optimization',[ValidateRange(10,128)][double]$MinimumFreeGiB=10)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$lease=[IO.File]::Open((Join-Path $repo 'data/perf/.asset-amqp-opt-extreme.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
$out=[IO.Path]::GetFullPath((Join-Path $repo $OutDir))
if(-not $out.StartsWith((Join-Path $repo 'data/perf/optimization-extreme-'),[StringComparison]::OrdinalIgnoreCase)){throw 'Separate ignored extreme archive required'}
if(Test-Path "$out/state.json"){throw 'Preserve earlier diagnostic; choose a new directory'}
$null=New-Item -ItemType Directory -Path $out -Force
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');target_rps=640;vus_per_scenario=512;warmup_seconds=60;steady_seconds=180;certification=$false;parent_experiment='optimization-extreme-20261019-v2';protocol='Same frozen mixed business; extra client VUs/sessions/fixtures; not application optimization'}
$state.script_hashes=@{};foreach($file in @('api.js','api-preauth.js','run.ps1','collect.ps1','extreme-client-diagnostic.ps1')){$state.script_hashes[$file]=(Get-FileHash "$PSScriptRoot/$file").Hash}
$state.minimum_free_gib=$MinimumFreeGiB
function Save {$state|ConvertTo-Json -Depth 10|Set-Content "$out/state.json"}
function Check([string]$Name){if($LASTEXITCODE -ne 0){throw "$Name failed"}}
$collector=$null;$touched=$false;$problem=$null
Save
try {
    if(Get-Process k6 -ErrorAction SilentlyContinue){throw 'Another load is active'}
    $os=Get-CimInstance Win32_OperatingSystem
    if([double]$os.FreePhysicalMemory/1MB -lt $MinimumFreeGiB){throw "Insufficient $MinimumFreeGiB GiB preflight headroom for doubled client; diagnostic not started"}
    $parent=Get-Content data/perf/optimization-extreme-20261019-v2/state.json -Raw|ConvertFrom-Json
    if($parent.status -ne 'completed' -or -not $parent.steps.final_restore){throw 'Primary controller must finish and restore first'}
    $frozen=Get-Content data/perf/optimization-p2/checkpoint-20261018T034834327/containers.jsonl|ForEach-Object{$_|ConvertFrom-Json}
    foreach($expected in $frozen|Where-Object{$_.name -match 'asset-amqp-opt-(gateway|iam-service|asset-service|governance-service|task-report-service|task-worker|task-publisher)-1$'}){
        $actual=& docker inspect $expected.name.TrimStart('/') --format '{{.Image}}';Check 'Frozen diagnostic image'
        if($actual.Trim() -ne $expected.image_id){throw 'Frozen application image changed'}
    }
    $env:OPT_IMAGE_TAG='p2-observe';$env:WORKER_SLOTS='2'
    foreach($file in @('d1.dump','k6-version.json')){Copy-Item "data/perf/optimization-p2/$file" "$out/$file"}
    if((Get-FileHash "$out/d1.dump").Hash.ToLowerInvariant() -ne 'f4e53123c4a642140cfe77799ba58046706a524b5c34002cff2acf1fc36d2e13'){throw 'D1 mismatch'}
    @{min_host_free_memory_ratio=0.05}|ConvertTo-Json|Set-Content "$out/resource-policy.json"
    & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -OutDir $out -EnvFile $EnvFile *> "$out/checkpoint-before.log";Check 'Diagnostic metadata'
    $arguments=@('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio','0.05')
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput "$out/collector.log" -RedirectStandardError "$out/collector.stderr.log"
    $state.status='running';$state.collector_pid=$collector.Id;Save
    $touched=$true
    & "$PSScriptRoot/run.ps1" -Preauthenticate -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -Mode mixed -Rates 640 -WarmupSeconds 60 -DurationSeconds 180 -Repetitions 1 -RunPrefix extreme-client512 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -RestoreSnapshot d1 -IndexProfile search -MaxVUs 512 -PreAllocatedVUs 512 *> "$out/load.log"
    $records=@(Get-Content "$out/api/extreme-client512-mixed-runs.json" -Raw|ConvertFrom-Json)
    if($records.Count -ne 1){throw 'Incomplete diagnostic'}
    $s=Get-Content $records[0].summary_path -Raw|ConvertFrom-Json
    if($s.preauthentication.sessions -ne 1024 -or $s.preauthentication.window_logins -ne 0 -or $s.preauthentication.window_refreshes -ne 0){throw 'Pure business session preparation failed'}
    if(-not(Get-Content $records[0].write_audit -Raw|ConvertFrom-Json).passed){throw 'Diagnostic SQL audit failed'}
    $state.record=$records[0];$state.status='pressure-completed';Save
} catch {$problem=$_;$state.status='interrupted';$state.error=$_.Exception.Message;Save}
finally {
    if($collector){'Stop diagnostic'|Set-Content "$out/telemetry/collector.stop";Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue}
    try {
        if($touched){
            & "$PSScriptRoot/database.ps1" -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers 1 *> "$out/final-restore.log";Check 'Diagnostic final restore'
            & "$PSScriptRoot/checkpoint.ps1" -Stage p2 -OutDir $out -EnvFile $EnvFile *> "$out/checkpoint-final.log";Check 'Diagnostic final metadata'
            $state.final_restore=$true
        }
    } catch {$state.restore_error=$_.Exception.Message;if(-not $problem){$problem=$_}}
    if(-not $problem){$state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')}
    Save;Remove-Item Env:OPT_IMAGE_TAG,Env:WORKER_SLOTS -ErrorAction SilentlyContinue;$lease.Dispose()
}
if($problem){throw $problem}
