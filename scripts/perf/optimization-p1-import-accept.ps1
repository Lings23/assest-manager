#requires -Version 7.2
# Real process crash/duplicate-message recovery follows the ordinary import matrix.
[CmdletBinding()]
param([string]$EnvFile='.env.optimization',[switch]$Resume)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-import'
$accept=@{status='waiting_matrix';started_utc=[DateTimeOffset]::UtcNow.ToString('o');error=$null}
$path=Join-Path $out 'acceptance-state.json'
if(Test-Path -LiteralPath $path){
    if(-not $Resume){throw 'Existing acceptance evidence; inspect it before another run'}
    $accept=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json -AsHashtable
    $accept.error=$null;$accept.status='resuming'
}
function Save-Accept {$accept|ConvertTo-Json -Depth 15|Set-Content -LiteralPath $path}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed; inspect preserved evidence"}}
function Restore([string]$Label){
    & (Join-Path $PSScriptRoot 'database.ps1') -Mode restore -Name d1 -OutDir $out -EnvFile $EnvFile -ComposeProject asset-amqp-opt -IndexProfile search *> (Join-Path $out "$Label-restore.log")
    Assert-Exit 'Accepted D1 restore'
}
Save-Accept
try {
    $deadline=[DateTimeOffset]::UtcNow.AddHours(5)
    while($true){
        $matrix=$null;try{$matrix=Get-Content -LiteralPath (Join-Path $out 'state.json') -Raw|ConvertFrom-Json}catch{}
        if($matrix.status -eq 'failed'){throw "P1-B matrix failed: $($matrix.error)"}
        if($matrix.status -eq 'completed'){break}
        if([DateTimeOffset]::UtcNow -gt $deadline){throw 'Import matrix wait timeout'}
        Start-Sleep -Seconds 5
    }
    Start-Sleep -Seconds 12
    $stop=Join-Path $out 'telemetry/collector.stop'
    if(Test-Path -LiteralPath $stop){Move-Item -LiteralPath $stop -Destination (Join-Path $out ('telemetry/matrix-stop-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff')))}
    $arguments=@('-NoProfile','-File',(Join-Path $PSScriptRoot 'collect.ps1'),'-EnvFile',$EnvFile,'-ComposeProject','asset-amqp-opt','-OutDir',(Join-Path $out 'telemetry'))
    $collector=Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $out 'fault-collector.log') -RedirectStandardError (Join-Path $out 'fault-collector.stderr.log')
    $accept.collector_pid=$collector.Id
    $env:OPT_IMAGE_TAG='p1-batch'
    if(-not $accept.compose_fault_passed){
    $accept.status='compose_fault';Save-Accept
    Restore fault
    $fault=Join-Path $out ('compose-fault-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
    & python (Join-Path $PSScriptRoot 'wide-import.py') --env-file $EnvFile --out-dir $fault --run-id p1b-real-worker-fault --case ordinary --rows 100000 --fault-worker --duplicate-deliveries 3 *> (Join-Path $out 'compose-fault.log')
    Assert-Exit 'Partial commit SIGKILL, duplicate AMQP delivery and lease recovery'
    $accept.compose_fault_path=$fault;$accept.compose_fault_passed=$true;Save-Accept
    }
    if(-not $accept.crud_passed){
    Restore crud
    $accept.status='seven_type_crud';Save-Accept
    $crud=Join-Path $out ('crud-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
    & python (Join-Path $PSScriptRoot 'asset-correctness.py') --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir $crud *> (Join-Path $out 'crud.log')
    Assert-Exit 'Real Gateway seven-type CRUD, scopes, concurrent lock and versions'
    $accept.crud_path=$crud;$accept.crud_passed=$true;Save-Accept
    }
    if(-not $accept.export_passed){
    Restore export
    $accept.status='seven_type_export';Save-Accept
    $exports=Join-Path $out ('exports-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
    & python (Join-Path $PSScriptRoot 'export-correctness.py') --manifest data/perf/20261016-local-baseline/seed-d1/seed-manifest.json --env-file $EnvFile --out-dir $exports *> (Join-Path $out 'exports.log')
    Assert-Exit 'Real CSV, MinIO, task idempotency and owner-only downloads'
    $accept.export_path=$exports;$accept.export_passed=$true;Save-Accept
    }
    Restore accepted
    & (Join-Path $PSScriptRoot 'checkpoint.ps1') -Stage p1-import -EnvFile $EnvFile *> (Join-Path $out 'checkpoint-accepted.log')
    'P1-B real process and content acceptance completed'|Set-Content -LiteralPath $stop
    $accept.status='completed';$accept.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-Accept
} catch {
    $accept.status='failed';$accept.error=$_.Exception.Message;Save-Accept
    if(Test-Path (Join-Path $out 'telemetry/abort.json')){& docker compose --env-file $EnvFile -p asset-amqp-opt stop --timeout 10 task-worker *> (Join-Path $out 'fault-resource-stop-worker.log')}
    throw
} finally {Remove-Item Env:OPT_IMAGE_TAG -ErrorAction SilentlyContinue}
