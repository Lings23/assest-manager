#requires -Version 7.2
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[string]$RunPrefix='p3-20261018',
    [ValidateSet('single2','single1','double2')][string[]]$Configurations=@('single2'))
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
if($RunPrefix -notmatch '^[a-zA-Z0-9_-]{1,16}$'){throw 'Unsafe experiment prefix'}
$out=Join-Path $repo 'data/perf/optimization-p3'
$null=New-Item -ItemType Directory -Force -Path $out
$stateFile=Join-Path $out "$RunPrefix-controller.json"
if(Test-Path $stateFile){throw 'Preserve earlier controller evidence; choose a new prefix'}
if(Test-Path "$out/telemetry/abort.json"){throw 'Existing resource abort requires investigation'}
if(Test-Path "$out/telemetry/collector.stop"){Move-Item -LiteralPath "$out/telemetry/collector.stop" -Destination "$out/telemetry/collector.stop.$RunPrefix"}
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');configurations=$Configurations;runs=@();error=$null}
function Save {$state|ConvertTo-Json -Depth 15|Set-Content $stateFile}
function Check([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; preserve the current backend before further experiments"}}
$priorImage=$env:OPT_IMAGE_TAG;$priorSlots=$env:WORKER_SLOTS
$collector=$null
Save
try {
    $env:OPT_IMAGE_TAG='p2-observe'
    @{min_host_free_memory_ratio=0.05;duration_seconds=30;reason='User explicitly lowered the host memory stop line to 5%; business gates unchanged'}|ConvertTo-Json|Set-Content "$out/resource-policy.json"
    & "$PSScriptRoot/checkpoint.ps1" -Stage p3 -EnvFile $EnvFile *> "$out/$RunPrefix-before.log";Check 'Before checkpoint'
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList @('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio','0.05') -WindowStyle Hidden -PassThru -RedirectStandardOutput "$out/$RunPrefix-collector.stdout.log" -RedirectStandardError "$out/$RunPrefix-collector.stderr.log"
    $state.collector_pid=$collector.Id;Save
    foreach($configuration in $Configurations){
        $workers=if($configuration -eq 'double2'){2}else{1}
        $slots=if($configuration -eq 'single1'){1}else{2}
        $repetitions=if($configuration -eq 'single2'){3}else{1}
        $points=if($configuration -eq 'single2'){@('control','statistics','import','export','both')}else{@('control','both')}
        $prefix="$RunPrefix-$configuration"
        $state.status="running-$configuration";Save
        & "$PSScriptRoot/interference.ps1" -OutDir $out -SeedManifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json -CandidatePath data/perf/20261016-local-baseline/api-20261017r2-candidate.json -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search -Workers $workers -Slots $slots -OnlineRPS 48 -FrozenSnapshotPath data/perf/20261016-local-baseline/s7base.dump -FrozenExportManifest data/perf/20261016-local-baseline/interference/s7-20261017/prepare-export/export-s7-20261017-prep-e.manifest.json -SnapshotName s7base -RunPrefix $prefix -Repetitions $repetitions -MaxVUs 64 -MinHostFreeMemoryRatio 0.05 -Points $points *> "$out/$prefix.log"
        Check "S7 $configuration"
        $result=Get-Content "$out/interference/$prefix/state.json" -Raw|ConvertFrom-Json
        if($result.status -ne 'completed'){throw 'S7 did not complete'}
        $state.runs+=@{configuration=$configuration;workers=$workers;slots=$slots;repetitions=$repetitions;points=$points;state="$out/interference/$prefix/state.json"};Save
    }
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save
}catch{$state.status='halted';$state.error=$_.Exception.Message;Save;throw}
finally{
    if($collector){'Controller finished'|Set-Content "$out/telemetry/collector.stop";Wait-Process -Id $collector.Id -Timeout 30 -ErrorAction SilentlyContinue}
    [Environment]::SetEnvironmentVariable('OPT_IMAGE_TAG',$priorImage,'Process')
    [Environment]::SetEnvironmentVariable('WORKER_SLOTS',$priorSlots,'Process')
}
