#requires -Version 7.2
[CmdletBinding()]
param(
    [ValidatePattern('^asset-multi-[a-z0-9-]+$')][string]$ComposeProject = ('asset-multi-' + [guid]::NewGuid().ToString('N').Substring(0,8)),
    [string]$EnvFile = '',
    [int]$Port = 59180,
    [switch]$SkipBuild,
    [switch]$KeepRunning
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
if (-not $EnvFile) {
    $directory = Join-Path $root "data/multi-instance/$ComposeProject"
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
    $EnvFile = Join-Path $directory '.env'
    if (-not (Test-Path -LiteralPath $EnvFile)) {
        $key = Join-Path $directory 'iam-private.pem'
        & (Join-Path $PSScriptRoot 'init-deployment.ps1') -KeyFile $key
        $lines = @('POSTGRES_PASSWORD','RABBITMQ_PASSWORD','MINIO_ROOT_PASSWORD','IAM_BOOTSTRAP_ADMIN_PASSWORD','INTERNAL_SERVICE_TOKEN') | ForEach-Object { "$_=$([guid]::NewGuid().ToString('N'))" }
        $lines += "IAM_PRIVATE_KEY_FILE=$($key.Replace('\','/'))", "MULTI_INSTANCE_PORT=$Port", "APP_SUBNET=172.28.$(Get-Random -Minimum 10 -Maximum 240).0/24", 'APP_VERSION=multi-instance-smoke'
        $lines | Set-Content -LiteralPath $EnvFile -Encoding utf8NoBOM
    }
}
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path
$composeArgs = @('compose','-p',$ComposeProject,'--env-file',$EnvFile,'-f','compose.yaml','-f','deploy/test/compose.multi-instance.yaml')
try {
    $configText = & docker @composeArgs config --format json
    if ($LASTEXITCODE -ne 0) { throw 'Compose configuration failed' }
    $config = $configText | ConvertFrom-Json
    foreach ($service in $config.services.PSObject.Properties) {
        if ($service.Name -ne 'service-lb' -and @($service.Value.ports).Where({ $null -ne $_ }).Count -gt 0) { throw "Unexpected published port: $($service.Name)" }
    }
    if (-not $SkipBuild) { & docker @composeArgs build; if ($LASTEXITCODE -ne 0) { throw 'Image build failed' } }
    & docker @composeArgs up -d --wait --wait-timeout 240
    if ($LASTEXITCODE -ne 0) { throw 'Isolated project did not become healthy' }
    & python -X utf8 (Join-Path $PSScriptRoot 'multi_instance_smoke.py') --project $ComposeProject --env-file $EnvFile
    if ($LASTEXITCODE -ne 0) { throw 'Multi-instance smoke failed' }
} finally {
    $evidence = Join-Path $root "data/multi-instance/$ComposeProject"
    New-Item -ItemType Directory -Force -Path $evidence | Out-Null
    & docker @composeArgs logs --no-color --tail 300 *> (Join-Path $evidence 'compose.log')
    if (-not $KeepRunning) { & docker @composeArgs stop }
}
