param(
    [string]$EnvFile = '',
    [string]$ComposeProject = 'asset-governance',
    [string]$BaseUrl = 'http://127.0.0.1:8080/api/v1'
)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
if (-not $EnvFile) { $EnvFile = Join-Path $root '.env' }
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path
$composeArgs = @('compose', '-f', (Join-Path $root 'compose.yaml'), '--env-file', $EnvFile, '-p', $ComposeProject)
$values = @{}
Get-Content -LiteralPath $EnvFile | ForEach-Object {
    if ($_ -match '^([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim() }
}
$base = $BaseUrl.TrimEnd('/')
$work = [IO.Path]::GetFullPath((Join-Path $root ('.phase3-migration-smoke-' + [guid]::NewGuid().ToString('N'))))
if (-not $work.StartsWith([IO.Path]::GetFullPath($root).TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'migration work directory escaped the repository' }
$source = Join-Path $work 'legacy.db'
$username = if ($values.IAM_BOOTSTRAP_ADMIN_USERNAME) { $values.IAM_BOOTSTRAP_ADMIN_USERNAME } else { 'admin' }
$originalPassword = $values.IAM_BOOTSTRAP_ADMIN_PASSWORD
if (-not $originalPassword) { throw "IAM_BOOTSTRAP_ADMIN_PASSWORD is missing from $EnvFile" }
$temporaryPassword = $originalPassword + '-MigrationSmoke!'
$passwordChanged = $false
$token = ''

function Invoke-Api([string]$Method, [string]$Path, $Body = $null, [string]$AccessToken = '') {
    $headers = @{}
    if ($AccessToken) { $headers.Authorization = 'Bearer ' + $AccessToken }
    $parameters = @{ Method = $Method; Uri = $base + $Path; Headers = $headers }
    if ($null -ne $Body) { $parameters.ContentType = 'application/json'; $parameters.Body = $Body | ConvertTo-Json -Depth 10 -Compress }
    Invoke-RestMethod @parameters
}

function Login([string]$Password) { Invoke-Api POST '/auth/login' @{ username = $username; password = $Password } }

function Run-Migration([string]$Type, [string]$Mode, [string]$Checkpoint, [switch]$DryRun) {
    $arguments = @('run', './tools/sqlite-migrate', '--source', $source, '--api', $base, '--asset-type', $Type,
        '--checkpoint', $Checkpoint, '--report', (Join-Path $work ($Mode + '-' + $Type + '.json')))
    if ($DryRun) { $arguments += '--dry-run' }
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw "$Mode migration failed for $Type" }
}

try {
    New-Item -ItemType Directory -Path $work | Out-Null
    $env:GOCACHE = Join-Path $root '.codex-go-cache'
    # Cached dependencies stay offline; retain checksum verification so a
    # caller-selected downloaded Go toolchain can be verified and launched.
    $env:GOWORK = 'off'; $env:GOPROXY = 'off'
    go run ./tools/sqlite-migrate/testdata/create_fixture.go -out $source
    if ($LASTEXITCODE -ne 0) { throw 'fixture creation failed' }

    $login = Login $originalPassword
    if ($login.user.must_change_password) {
        $token = $login.access_token
        Invoke-Api POST '/auth/change-password' @{ current_password = $originalPassword; new_password = $temporaryPassword } $token | Out-Null
        $passwordChanged = $true
        $login = Login $temporaryPassword
    }
    $token = $login.access_token
    $env:ASSET_MIGRATION_TOKEN = $token

    $checkpoint1 = Join-Path $work 'first-checkpoint.json'
    Run-Migration 'responsible-department' 'dry' $checkpoint1 -DryRun
    Run-Migration 'data' 'dry' $checkpoint1 -DryRun
    Run-Migration 'responsible-department' 'first' $checkpoint1
    Run-Migration 'data' 'first' $checkpoint1

    # A fresh checkpoint proves target-side idempotency, not just local resume state.
    $checkpoint2 = Join-Path $work 'fresh-checkpoint.json'
    Run-Migration 'responsible-department' 'repeat' $checkpoint2
    Run-Migration 'data' 'repeat' $checkpoint2

    $env:INTERNAL_SERVICE_TOKEN = if ($env:INTERNAL_SERVICE_TOKEN) { $env:INTERNAL_SERVICE_TOKEN } else { $values.INTERNAL_SERVICE_TOKEN }
    $evidence = & docker @composeArgs exec -T postgres psql -U $values.POSTGRES_USER -d $values.POSTGRES_DB -Atc "SELECT (SELECT count(*) FROM asset.legacy_migrations WHERE legacy_id IN ('900001','900002')),(SELECT count(*) FROM asset.responsible_department_assets WHERE department_code='P3-MIGRATION'),(SELECT count(*) FROM asset.data_assets WHERE data_name='第三阶段迁移样本数据' AND security_level=1 AND is_cross_border=true AND deleted_at IS NOT NULL);"
    if ($LASTEXITCODE -ne 0) { throw 'migration evidence query failed' }
    $parts = $evidence.Trim().Split('|')
    if ($parts.Count -ne 3 -or $parts[0] -ne '2' -or $parts[1] -ne '1' -or $parts[2] -ne '1') { throw "migration evidence mismatch: $evidence" }
    Write-Output "Phase 3 migration smoke passed: legacy_keys=$($parts[0]) departments=$($parts[1]) corrected_soft_deleted_data=$($parts[2]) repeat_created=0"
}
finally {
    Remove-Item Env:ASSET_MIGRATION_TOKEN -ErrorAction SilentlyContinue
    if ($passwordChanged -and $token) {
        try { Invoke-Api POST '/auth/change-password' @{ current_password = $temporaryPassword; new_password = $originalPassword } $token | Out-Null } catch { Write-Warning 'Could not restore the integration admin password' }
    }
    if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}
