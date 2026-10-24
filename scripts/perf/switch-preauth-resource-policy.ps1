#requires -Version 7.2
# Collector-only handoff at an already completed point; never interrupts k6.
[CmdletBinding()]
param([string]$OutDir='data/perf/optimization-p2/preauthenticated-20261018',[int]$AfterRate=320)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=(Resolve-Path -LiteralPath $OutDir).Path
if(-not $out.StartsWith((Join-Path $repo 'data/perf/optimization-p2/preauthenticated-'),[StringComparison]::OrdinalIgnoreCase)){throw 'Collector handoff is restricted to this isolated new protocol'}
$record="$out/api/preauth-step-$AfterRate-mixed-runs.json"
while(-not(Test-Path $record)){
    $state=Get-Content "$out/state.json" -Raw|ConvertFrom-Json
    if($state.status -eq 'failed'){throw 'Controller failed before the requested boundary'}
    Start-Sleep -Milliseconds 500
}
$point=@(Get-Content $record -Raw|ConvertFrom-Json)
if(-not $point[-1].load_point_passed){throw 'No further escalation after a failed point; leave resource policy handoff unexecuted'}
$state=Get-Content "$out/state.json" -Raw|ConvertFrom-Json
$oldId=[int]$state.collector_pid
$process=Get-CimInstance Win32_Process -Filter "ProcessId=$oldId"
if($process -and ($process.CommandLine -notmatch 'collect\.ps1' -or -not $process.CommandLine.Contains($out,[StringComparison]::OrdinalIgnoreCase))){throw 'Collector identity changed; no action'}
$stop="$out/telemetry/collector.stop"
"Explicit user-requested host threshold handoff at completed$AfterRate RPS boundary"|Set-Content $stop
$deadline=[DateTimeOffset]::UtcNow.AddSeconds(30)
while(Get-Process -Id $oldId -ErrorAction SilentlyContinue){
    if([DateTimeOffset]::UtcNow -gt $deadline){throw 'Collector did not stop gracefully; no duplicate collector started'}
    Start-Sleep -Milliseconds 250
}
Move-Item -LiteralPath $stop -Destination "$out/telemetry/policy-handoff-stop-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))"
$arguments=@('-NoProfile','-File',"$PSScriptRoot/collect.ps1",'-EnvFile','.env.optimization','-ComposeProject','asset-amqp-opt','-OutDir',"$out/telemetry",'-Enhanced','-MinHostFreeMemoryRatio','0.05')
$next=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru
@{utc=[DateTimeOffset]::UtcNow.ToString('o');after_point=$point[-1].run_id;old_collector_pid=$oldId;new_collector_pid=$next.Id;min_host_free_memory_ratio=0.05;previous_ratio=0.15;duration_seconds=30;application_restarted=$false;inflight_k6_interrupted=$false;source='Explicit user instruction';collector_script_sha256=(Get-FileHash "$PSScriptRoot/collect.ps1").Hash;run_script_sha256=(Get-FileHash "$PSScriptRoot/run.ps1").Hash}|ConvertTo-Json|Set-Content "$out/resource-policy-handoff.json"
Write-Output "Host memory5% collector started after completed$AfterRate RPS point; prior15% collector exited gracefully."
