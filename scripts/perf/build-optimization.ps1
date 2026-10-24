#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^p[0-3][a-z0-9-]*$')][string]$StageTag,
    [Parameter(Mandatory)][string]$OutDir,
    [ValidateSet('asset-service','iam-service','gateway','governance-service','task-report-service','task-worker','task-publisher','task-migrate')][string[]]$Services=@('asset-service')
)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$OutDir=[IO.Path]::GetFullPath($OutDir)
$allowed=[IO.Path]::GetFullPath((Join-Path $repo 'data/perf/optimization-'))
if(-not $OutDir.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'Build evidence must be in an optimization archive'}
if(@(Get-Process -Name k6 -ErrorAction SilentlyContinue).Count){throw 'Do not build while a load generator is active'}
$os=Get-CimInstance Win32_OperatingSystem
if([double]$os.FreePhysicalMemory/[double]$os.TotalVisibleMemorySize -lt 0.15){throw 'Host memory below original 15% resource guard; no build started'}
$buildDir=Join-Path $OutDir ('build-'+$StageTag+'-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
New-Item -ItemType Directory -Path $buildDir -Force|Out-Null
$base='golang@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946'
$baseID=& docker image inspect $base --format '{{.Id}}'
if($LASTEXITCODE -ne 0){throw 'Pinned Go1.25.13 image is not cached'}
& docker tag $base asset-amqp-opt-build:go1.25.13
if($LASTEXITCODE -ne 0){throw 'Could not alias the pinned local builder'}
$dockerfile=[IO.File]::ReadAllText((Join-Path $repo 'platform/Dockerfile')).Replace('golang:1.25.13-alpine3.24@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946','asset-amqp-opt-build:go1.25.13')
$path=Join-Path $buildDir 'Dockerfile-local'
[IO.File]::WriteAllText($path,$dockerfile)
$record=@{started_utc=[DateTimeOffset]::UtcNow.ToString('o');stage=$StageTag;builder_image_id=$baseID;flags='CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w"';services=@();status='building'}
foreach($service in $Services){
    $command=if($service -eq 'task-migrate'){'task-report-service'}else{$service}
    & docker build --pull=false -f $path --build-arg "SERVICE=$command" -t "asset-amqp-opt-${service}:latest" platform *> (Join-Path $buildDir "$service.log")
    if($LASTEXITCODE -ne 0){$record.status='failed';$record|ConvertTo-Json -Depth 8|Set-Content (Join-Path $buildDir 'state.json');throw "Build failed: $service"}
    $image=& docker image inspect "asset-amqp-opt-${service}:latest" --format '{{.Id}}'
    $binary=& docker run --rm --pull=never --entrypoint sha256sum "asset-amqp-opt-${service}:latest" /usr/local/bin/service
    if($LASTEXITCODE -ne 0){throw 'Binary fingerprint failed'}
    $record.services+=@{name=$service;image_id=$image;binary_sha256=($binary -split '\s+')[0]}
}
foreach($service in @('asset-service','iam-service','gateway','governance-service','task-report-service','task-worker','task-publisher','task-migrate','web')){
    & docker tag "asset-amqp-opt-${service}:latest" "asset-amqp-opt-${service}:$StageTag"
    if($LASTEXITCODE -ne 0){throw "Stage image tag failed: $service"}
}
$record.status='completed';$record.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
$record|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $buildDir 'state.json')
Write-Output "Build evidence: $buildDir"
