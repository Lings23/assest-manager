# Continue authorized experiments only after the full single-instance suite ends.
# No mutation is performed while the first controller or its collector is active.
#requires -Version 7.2
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p3'
$baseline="$out/p3-20261018-controller.json"
while($true){
    $state=Get-Content $baseline -Raw|ConvertFrom-Json
    if($state.status -eq 'halted'){throw 'Baseline halted; do not start comparisons'}
    if($state.status -eq 'completed'){break}
    Start-Sleep -Seconds 5
}
for($i=0;$i -lt 12;$i++){
    if(-not(Get-Process -Id $state.collector_pid -ErrorAction SilentlyContinue)){break}
    Start-Sleep -Seconds 5
}
if(Get-Process -Id $state.collector_pid -ErrorAction SilentlyContinue){throw 'Earlier collector did not exit'}
if(Get-Process k6 -ErrorAction SilentlyContinue){throw 'A load client remains active'}
& "$PSScriptRoot/optimization-p3.ps1" -RunPrefix p3-compare1 -Configurations single1
if($LASTEXITCODE -ne 0){throw 'Single-slot comparison failed'}
& "$PSScriptRoot/worker-runtime-proof.ps1" -ExpectedWorkers 1 -ExpectedSlots 1 -Output data/perf/optimization-p3/single1-runtime.json
& "$PSScriptRoot/optimization-p3.ps1" -RunPrefix p3-compare2 -Configurations double2
if($LASTEXITCODE -ne 0){throw 'Two-worker comparison failed'}
& "$PSScriptRoot/worker-runtime-proof.ps1" -ExpectedWorkers 2 -ExpectedSlots 2 -Output data/perf/optimization-p3/double2-runtime.json
Write-Output 'Authorized P3 configuration comparisons completed.'
