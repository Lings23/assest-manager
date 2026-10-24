# Read only: verify actual isolated worker replica count, image and slot env.
param([ValidateRange(1,2)][int]$ExpectedWorkers,[ValidateRange(1,4)][int]$ExpectedSlots,[Parameter(Mandatory)][string]$Output)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$dest=[IO.Path]::GetFullPath((Join-Path $repo $Output))
if(-not $dest.StartsWith((Join-Path $repo 'data/perf/optimization-p3/'),[StringComparison]::OrdinalIgnoreCase)){throw 'Only P3 evidence is writable'}
if(Test-Path $dest){throw 'Preserve prior runtime proof'}
$ids=@(& docker ps -q --filter label=com.docker.compose.project=asset-amqp-opt --filter label=com.docker.compose.service=task-worker)
if($LASTEXITCODE -ne 0 -or $ids.Count -ne $ExpectedWorkers){throw 'Unexpected actual worker replica count'}
$rows=@(foreach($id in $ids){
    $raw=& docker inspect $id --format '{"name":{{json .Name}},"image_id":{{json .Image}},"health":{{json .State.Health.Status}}}'
    if($LASTEXITCODE -ne 0){throw 'Worker inspect failed'}
    $record=$raw|ConvertFrom-Json -AsHashtable
    $slots=& docker exec $id printenv WORKER_SLOTS
    if($LASTEXITCODE -ne 0 -or [int]$slots.Trim() -ne $ExpectedSlots -or $record.health -ne 'healthy'){throw 'Worker slot/health evidence differs from plan'}
    $record.slots=[int]$slots.Trim();$record
})
@{utc=[DateTimeOffset]::UtcNow.ToString('o');passed=$true;actual_workers=$ids.Count;slots_per_worker=$ExpectedSlots;instances=$rows}|ConvertTo-Json -Depth 8|Set-Content $dest
Write-Output "Verified workers=$($ids.Count), slots=$ExpectedSlots; $dest"
