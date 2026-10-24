#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$ComposeProject,
    [Parameter(Mandatory)][string]$EnvFile,
    [Parameter(Mandatory)][ValidateSet('iam-service','asset-service','task-report-service','governance-service')][string]$Service,
    [string[]]$ComposeFiles = @('compose.yaml'),
    [switch]$SkipBuild
)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Split-Path -Parent $PSScriptRoot)
$composeArgs = @('compose','-p',$ComposeProject,'--env-file',(Resolve-Path -LiteralPath $EnvFile).Path)
foreach ($file in $ComposeFiles) { $composeArgs += @('-f',$file) }
function Invoke-Compose([string[]]$Arguments) {
    $result = & docker @composeArgs @Arguments
    if ($LASTEXITCODE -ne 0) { throw 'Compose operation failed' }
    $result
}
function Wait-Healthy([string]$Container) {
    $deadline = (Get-Date).AddMinutes(2)
    do {
        $state = & docker inspect -f '{{.State.Health.Status}}' $Container
        if ($LASTEXITCODE -ne 0) { throw "Container unavailable: $Container" }
        if ($state -eq 'healthy') { Start-Sleep -Seconds 5; return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "New instance did not become healthy: $Container"
}
$original = @(Invoke-Compose -Arguments @('ps','-q',$Service))
if ($original.Count -ge 8) { throw 'HAProxy has 8 discovery slots; reserve one for the canary' }
if ($original.Count -lt 2) { throw 'Rolling deployment requires at least two healthy instances' }
foreach ($container in $original) {
    $owner = & docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' $container
    if ($owner -ne $ComposeProject) { throw 'Container ownership mismatch' }
    Wait-Healthy $container
}
if (-not $SkipBuild) { Invoke-Compose -Arguments @('build',$Service) }
# Existing schema must already support both versions. The new canary verifies it
# before any old instance is stopped. Never run destructive migrations here.
$canary = "$ComposeProject-$Service-roll-$([guid]::NewGuid().ToString('N').Substring(0,8))"
$started = $false
$readyCanary = $false
$completed = $false
try {
    Invoke-Compose -Arguments @('run','-d','--rm','--no-deps','--use-aliases','--name',$canary,$Service) | Out-Null
    $started = $true
    Wait-Healthy $canary
    $readyCanary = $true
    foreach ($old in $original) {
        & docker stop $old | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Old instance did not drain' }
        & docker rm $old | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Old instance could not be removed' }
        Invoke-Compose -Arguments @('up','-d','--no-deps','--no-build','--no-recreate','--scale',"$Service=$($original.Count)",$Service) | Out-Null
        foreach ($replacement in @(Invoke-Compose -Arguments @('ps','-q',$Service))) { Wait-Healthy $replacement }
    }
    $completed = $true
} finally {
    if ($started -and ($completed -or -not $readyCanary)) { & docker stop $canary | Out-Null }
    elseif ($started) { Write-Warning "Healthy temporary instance retained after failed rollout: $canary" }
}
Write-Output "Rolled ${Service}: $($original.Count) instances; one temporary instance added to the connection budget."
