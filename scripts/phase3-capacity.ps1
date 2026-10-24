param(
    [int]$Rows = 100000,
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
Get-Content -LiteralPath $EnvFile | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim() } }
$base = $BaseUrl.TrimEnd('/')
$loginBody = @{ username = ($values.IAM_BOOTSTRAP_ADMIN_USERNAME ?? 'admin'); password = $values.IAM_BOOTSTRAP_ADMIN_PASSWORD } | ConvertTo-Json -Compress
$login = Invoke-RestMethod -Method Post -Uri ($base + '/auth/login') -ContentType 'application/json' -Body $loginBody
if ($login.user.must_change_password) { throw 'capacity test requires an initialized admin account; run phase3-compose-smoke.ps1 first' }
$headers = @{ Authorization = 'Bearer ' + $login.access_token }
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$builder = [Text.StringBuilder]::new([Math]::Max(1024, $Rows * 32))
[void]$builder.Append("department_name,department_code`n")
for ($index = 0; $index -lt $Rows; $index++) { [void]$builder.Append("容量部门$suffix-$index,C$suffix-$index`n") }
$key = "phase3-capacity-$suffix-$Rows"
$body = @{ asset_type = 'responsible-department'; idempotency_key = $key; csv = $builder.ToString() } | ConvertTo-Json -Compress
$watch = [Diagnostics.Stopwatch]::StartNew()
$created = Invoke-RestMethod -Method Post -Uri ($base + '/import-jobs') -Headers $headers -ContentType 'application/json' -Body $body
if ($created.task.result.total -ne $Rows -or $created.task.result.valid -ne $Rows -or $created.task.result.error_count -ne 0) { throw "100k preflight result mismatch: total=$($created.task.result.total) valid=$($created.task.result.valid) errors=$($created.task.result.error_count)" }
if ($created.task.input.csv -or -not $created.task.input.source_object_key) { throw '100k import source was not staged in MinIO' }
Invoke-RestMethod -Method Post -Uri ($base + '/import-jobs/' + $created.task.id + '/confirm') -Headers $headers -ContentType 'application/json' -Body '{}' | Out-Null
$deadline = (Get-Date).AddMinutes(10)
do {
    $task = (Invoke-RestMethod -Uri ($base + '/tasks/' + $created.task.id) -Headers $headers).task
    if ($task.state -in @('succeeded', 'dead_letter')) { break }
    Start-Sleep -Seconds 1
} while ((Get-Date) -lt $deadline)
$watch.Stop()
if ($task.state -ne 'succeeded' -or $task.result.committed -ne $Rows -or $task.result.created -ne $Rows -or $task.result.skipped -ne 0 -or $task.attempt -ne 1) { throw "capacity task failed: state=$($task.state) committed=$($task.result.committed) created=$($task.result.created) skipped=$($task.result.skipped) attempt=$($task.attempt) error=$($task.last_error)" }
$again = Invoke-RestMethod -Method Post -Uri ($base + '/import-jobs') -Headers $headers -ContentType 'application/json' -Body $body
if ($again.created -ne $false -or $again.task.id -ne $created.task.id) { throw 'capacity task replay was not idempotent' }
if (-not $env:INTERNAL_SERVICE_TOKEN) { $env:INTERNAL_SERVICE_TOKEN = $values.INTERNAL_SERVICE_TOKEN }
$evidence = & docker @composeArgs exec -T postgres psql -U $values.POSTGRES_USER -d $values.POSTGRES_DB -Atc "SELECT (SELECT count(*) FROM asset.import_idempotency WHERE task_id='$($created.task.id)'),(SELECT count(*) FROM task_report.import_rows WHERE task_id='$($created.task.id)' AND committed_at IS NOT NULL);"
if ($LASTEXITCODE -ne 0) { throw 'capacity evidence query failed' }
$parts = $evidence.Trim().Split('|')
if ([int]$parts[0] -ne $Rows -or [int]$parts[1] -ne $Rows) { throw "capacity database mismatch: $evidence" }
Write-Output "Phase 3 capacity passed: rows=$Rows seconds=$([Math]::Round($watch.Elapsed.TotalSeconds,2)) task=$($created.task.id) import_keys=$($parts[0]) committed_rows=$($parts[1])"
