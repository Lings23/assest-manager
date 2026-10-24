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
if (-not $env:INTERNAL_SERVICE_TOKEN) { $env:INTERNAL_SERVICE_TOKEN = $values.INTERNAL_SERVICE_TOKEN }
$base = $BaseUrl.TrimEnd('/')
$rabbitPort = if ($values.RABBITMQ_MANAGEMENT_PORT) { $values.RABBITMQ_MANAGEMENT_PORT } else { '15672' }
$rabbitBase = 'http://127.0.0.1:' + $rabbitPort
$username = if ($values.IAM_BOOTSTRAP_ADMIN_USERNAME) { $values.IAM_BOOTSTRAP_ADMIN_USERNAME } else { 'admin' }
$originalPassword = $values.IAM_BOOTSTRAP_ADMIN_PASSWORD
if (-not $originalPassword) { throw "IAM_BOOTSTRAP_ADMIN_PASSWORD is missing from $EnvFile" }
$temporaryPassword = $originalPassword + '-Phase3!'
$passwordChanged = $false
$token = ''

function Invoke-Json([string]$Method, [string]$Path, $Body = $null, [string]$AccessToken = '') {
    $headers = @{}
    if ($AccessToken) { $headers.Authorization = 'Bearer ' + $AccessToken }
    $parameters = @{ Method = $Method; Uri = $base + $Path; Headers = $headers }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = $Body | ConvertTo-Json -Depth 20 -Compress
    }
    Invoke-RestMethod @parameters
}

function Login([string]$Password) {
    Invoke-Json POST '/auth/login' @{ username = $username; password = $Password }
}

function Get-ResponseText($Response) {
    if ($Response.Content -is [byte[]]) { return [Text.Encoding]::UTF8.GetString($Response.Content) }
    [string]$Response.Content
}

function Wait-Task([string]$Id, [string[]]$Terminal = @('succeeded', 'dead_letter'), [int]$Seconds = 45) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $response = Invoke-Json GET ('/tasks/' + $Id) $null $token
        if ($Terminal -contains $response.task.state) { return $response.task }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "task $Id did not reach $($Terminal -join '/')"
}

try {
    $login = Login $originalPassword
    if ($login.user.must_change_password) {
        $token = $login.access_token
        Invoke-Json POST '/auth/change-password' @{ current_password = $originalPassword; new_password = $temporaryPassword } $token | Out-Null
        $passwordChanged = $true
        $login = Login $temporaryPassword
    }
    $token = $login.access_token

    $suffix = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $departmentName = 'Phase3-' + $suffix
    $departmentCode = 'P3' + $suffix
    $csv = "department_name,department_code
$departmentName,$departmentCode
"
    $idempotencyKey = 'phase3-import-' + $suffix

    $created = Invoke-Json POST '/import-jobs' @{ asset_type = 'responsible-department'; idempotency_key = $idempotencyKey; csv = $csv } $token
    if ($created.task.state -ne 'awaiting_confirmation' -or $created.task.result.valid -ne 1) { throw 'import preflight did not pass' }
    if ($created.task.input.csv -or -not $created.task.input.source_object_key -or -not $created.task.input.source_object_key.StartsWith('imports/')) { throw 'import source was not staged safely in MinIO' }
    Invoke-Json POST ('/import-jobs/' + $created.task.id + '/confirm') @{} $token | Out-Null
    $importTask = Wait-Task $created.task.id
    if ($importTask.state -ne 'succeeded' -or $importTask.result.created -ne 1 -or $importTask.result.committed -ne 1) { throw 'import worker did not create and commit exactly one row' }

    $again = Invoke-Json POST '/import-jobs' @{ asset_type = 'responsible-department'; idempotency_key = $idempotencyKey; csv = $csv } $token
    if ($again.created -ne $false -or $again.task.id -ne $created.task.id) { throw 'task idempotency key was not stable' }

    $invalidCsv = "department_name,department_code
,INVALID
"
    $invalid = Invoke-Json POST '/import-jobs' @{ asset_type = 'responsible-department'; idempotency_key = ('phase3-invalid-' + $suffix); csv = $invalidCsv } $token
    if ($invalid.task.state -ne 'awaiting_confirmation' -or $invalid.task.result.error_count -lt 1 -or -not $invalid.task.result.error_object_key) { throw 'invalid import did not create a MinIO error report' }
    $errorDownload = Invoke-WebRequest -Uri ($base + '/import-jobs/' + $invalid.task.id + '/errors') -Headers @{ Authorization = 'Bearer ' + $token }
    $errorText = Get-ResponseText $errorDownload
    if ($errorDownload.StatusCode -ne 200 -or -not $errorText.Contains('department_name')) { throw 'import error report download is invalid' }

    $assetList = Invoke-Json GET ('/assets/responsible-department?search=' + [uri]::EscapeDataString($departmentCode) + '&limit=20') $null $token
    if (@($assetList.items).Count -ne 1) { throw 'imported asset was not found exactly once' }

    $export = Invoke-Json POST '/export-jobs' @{ asset_type = 'responsible-department'; idempotency_key = ('phase3-export-' + $suffix) } $token
    $exportTask = Wait-Task $export.task.id
    if ($exportTask.result.rows -lt 1) { throw 'export produced no rows' }
    $exportPattern = '^exports/' + [regex]::Escape($export.task.id) + '/[0-9a-fA-F-]{36}\.csv$'
    if ($exportTask.result.object_key -notmatch $exportPattern) { throw 'export object key is not isolated by execution token' }
    $download = Invoke-WebRequest -Uri ($base + '/export-jobs/' + $export.task.id + '/download') -Headers @{ Authorization = 'Bearer ' + $token }
    $downloadText = Get-ResponseText $download
    if ($download.StatusCode -ne 200 -or -not $downloadText.Contains('department_name') -or -not $downloadText.Contains($departmentName)) { throw 'export download content is invalid' }

    $report = Invoke-Json GET '/reporting/asset-counts' $null $token
    $responsibleCount = @($report.items | Where-Object asset_type -eq 'responsible-department')
    if ($responsibleCount.Count -ne 1 -or $responsibleCount[0].total -lt 1) { throw 'permission-filtered reporting count is invalid' }

    $rabbitUser = if ($values.RABBITMQ_USER) { $values.RABBITMQ_USER } else { 'asset' }
    $rabbitCredential = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($rabbitUser + ':' + $values.RABBITMQ_PASSWORD))
    $rabbitHeaders = @{ Authorization = 'Basic ' + $rabbitCredential }
    $previousDeadQueue = Invoke-RestMethod -Uri ($rabbitBase + '/api/queues/%2F/task-report-worker.dead') -Headers $rabbitHeaders
    $bad = Invoke-Json POST '/export-jobs' @{ asset_type = 'not-a-real-type'; idempotency_key = ('phase3-dead-' + $suffix) } $token
    $dead = Wait-Task $bad.task.id @('dead_letter') 45
    if ($dead.attempt -ne 1) { throw "expected permanent failure after 1 attempt, got $($dead.attempt)" }

    $workerQueue = Invoke-RestMethod -Uri ($rabbitBase + '/api/queues/%2F/task-report-worker') -Headers $rabbitHeaders
    $allConsumers = Invoke-RestMethod -Uri ($rabbitBase + '/api/consumers/%2F') -Headers $rabbitHeaders
    $consumers = @($allConsumers | Where-Object { $_.queue.name -eq 'task-report-worker' })
    if ($consumers.Count -lt 1 -or @($consumers | Where-Object { -not $_.ack_required -or $_.prefetch_count -ne 1 -or $_.exclusive }).Count -gt 0) { throw 'AMQP worker consumers must use shared subscriptions, manual ACK and prefetch=1' }
    $deadDeadline = (Get-Date).AddSeconds(20)
    do {
        # Management statistics refresh asynchronously; resample both queues.
        $workerQueue = Invoke-RestMethod -Uri ($rabbitBase + '/api/queues/%2F/task-report-worker') -Headers $rabbitHeaders
        $deadQueue = Invoke-RestMethod -Uri ($rabbitBase + '/api/queues/%2F/task-report-worker.dead') -Headers $rabbitHeaders
        if ($workerQueue.messages -eq 0 -and $deadQueue.messages -ge ($previousDeadQueue.messages + 1)) { break }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadDeadline)
    if ($workerQueue.messages -ne 0 -or $deadQueue.messages -lt ($previousDeadQueue.messages + 1)) { throw 'RabbitMQ queue/dead-letter evidence is invalid' }

    $smokeTaskIds = "'" + (@($created.task.id, $export.task.id, $bad.task.id) -join "','") + "'"
    $databaseEvidence = & docker @composeArgs exec -T postgres psql -U $values.POSTGRES_USER -d $values.POSTGRES_DB -Atc "SELECT (SELECT count(*) FROM task_report.inbox i JOIN task_report.outbox o ON i.event_id=o.event_id WHERE i.consumer='task-report-worker-v2' AND o.topic='task.requested' AND o.aggregate_id IN ($smokeTaskIds)),(SELECT count(*) FROM task_report.dead_letters WHERE task_id='$($bad.task.id)'),(SELECT count(*) FROM asset.import_idempotency WHERE task_id='$($created.task.id)'),(SELECT count(*) FROM task_report.permission_counts WHERE requester_id='$($created.task.requested_by)'),(SELECT count(*) FROM task_report.import_rows WHERE task_id='$($created.task.id)' AND committed_at IS NOT NULL),(SELECT count(*) FROM task_report.outbox WHERE aggregate_id='$($bad.task.id)' AND topic='task.dead' AND published_at IS NOT NULL);"
    if ($LASTEXITCODE -ne 0) { throw 'database evidence query failed' }
    $parts = $databaseEvidence.Trim().Split('|')
    if ($parts.Count -ne 6 -or [int]$parts[0] -ne 3 -or [int]$parts[1] -ne 1 -or [int]$parts[2] -ne 1 -or [int]$parts[3] -lt 7 -or [int]$parts[4] -ne 1 -or [int]$parts[5] -ne 1) { throw "database evidence is incomplete: $databaseEvidence" }

    Write-Output "Phase 3 Compose smoke passed: import=1 committed=$($parts[4]) export_rows=$($exportTask.result.rows) token_key=$($exportTask.result.object_key) consumers=$($consumers.Count) manual_ack=true prefetch=1 attempts=$($dead.attempt) inbox=$($parts[0]) dead=$($parts[1]) import_keys=$($parts[2]) report_rows=$($parts[3])"
}
finally {
    if ($passwordChanged -and $token) {
        try { Invoke-Json POST '/auth/change-password' @{ current_password = $temporaryPassword; new_password = $originalPassword } $token | Out-Null } catch { Write-Warning 'Could not restore the integration admin password' }
    }
}

