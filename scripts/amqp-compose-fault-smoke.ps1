param(
    [string]$EnvFile = 'deploy/test/amqp.env',
    [string]$ComposeProject = 'asset-amqp-acceptance',
    [string]$BaseUrl = 'http://127.0.0.1:58080/api/v1'
)
$ErrorActionPreference = 'Stop'
# This script interrupts services, so restrict it to isolated acceptance projects.
if (-not $ComposeProject.StartsWith('asset-amqp-')) { throw 'Use an isolated asset-amqp-* project for fault injection' }
$root = Split-Path -Parent $PSScriptRoot
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path
$composeArgs = @('compose', '-f', (Join-Path $root 'compose.yaml'), '--env-file', $EnvFile, '-p', $ComposeProject)
$values = @{}
Get-Content -LiteralPath $EnvFile | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim() } }
$base = $BaseUrl.TrimEnd('/')
$login = Invoke-RestMethod -Method Post -Uri ($base + '/auth/login') -ContentType 'application/json' -Body (@{username=$values.IAM_BOOTSTRAP_ADMIN_USERNAME;password=$values.IAM_BOOTSTRAP_ADMIN_PASSWORD}|ConvertTo-Json)
if ($login.user.must_change_password) { throw 'Run phase3-compose-smoke.ps1 first to initialize the test admin' }
$headers = @{Authorization='Bearer '+$login.access_token}
$rabbitPort = if ($values.RABBITMQ_MANAGEMENT_PORT) { $values.RABBITMQ_MANAGEMENT_PORT } else { '15672' }
$rabbitBase = 'http://127.0.0.1:' + $rabbitPort + '/api'
$rabbitHeaders = @{Authorization='Basic '+[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($values.RABBITMQ_USER+':'+$values.RABBITMQ_PASSWORD))}
$rabbitStopped = $false; $postgresStopped = $false; $minioStopped = $false

function Compose([string[]]$Arguments) {
    & docker @composeArgs @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Compose failed: $($Arguments -join ' ')" }
}
function Sql([string]$Statement) {
    $result = & docker @composeArgs exec -T postgres psql -U $values.POSTGRES_USER -d $values.POSTGRES_DB -Atc $Statement
    if ($LASTEXITCODE -ne 0) { throw 'Database evidence query failed' }
    ($result -join "`n").Trim()
}
function Create-Export {
    (Invoke-RestMethod -Method Post -Uri ($base+'/export-jobs') -Headers $headers -ContentType 'application/json' -Body (@{asset_type='responsible-department';idempotency_key=('amqp-fault-'+[guid]::NewGuid())}|ConvertTo-Json)).task
}
function Wait-Task([string]$Id,[string]$State,[int]$Seconds=180) {
    $deadline=(Get-Date).AddSeconds($Seconds)
    do {
        $task=(Invoke-RestMethod -Uri ($base+'/tasks/'+$Id) -Headers $headers).task
        if ($task.state -eq $State) { return $task }
        if ($task.state -eq 'dead_letter') { throw "Task failed: $($task.last_error)" }
        Start-Sleep -Milliseconds 100
    } while ((Get-Date) -lt $deadline)
    throw "Task $Id did not reach $State"
}
try {
    Compose -Arguments @('stop','rabbitmq') | Out-Null; $rabbitStopped=$true
    Compose -Arguments @('exec','-T','task-report-service','wget','-qO-','http://127.0.0.1:8084/health/ready') | Out-Null
    $duringOutage=Create-Export
    $pending=Wait-Task $duringOutage.id 'pending' 10
    if ($pending.attempt -ne 0) { throw 'Task executed while RabbitMQ was stopped' }
    $pendingEvents=Sql "SELECT count(*) FROM task_report.outbox WHERE aggregate_id='$($pending.id)' AND published_at IS NULL AND superseded_at IS NULL;"
    if ($pendingEvents -ne '1') { throw 'Rabbit outage did not retain its outbox event' }
    Compose -Arguments @('start','rabbitmq') | Out-Null; $rabbitStopped=$false
    $resumed=Wait-Task $pending.id 'succeeded'
    Write-Output "RabbitMQ outage passed: api_ready=true pending_outbox=1 resumed_attempt=$($resumed.attempt)"

    Compose -Arguments @('stop','postgres') | Out-Null; $postgresStopped=$true
    $poison='amqp-poison-'+[guid]::NewGuid().ToString('N')
    $event=[guid]::NewGuid().ToString()
    $publish=@{properties=@{delivery_mode=2;message_id=$event;headers=@{event_id=$event}};routing_key='task.requested';payload=$poison;payload_encoding='string'}|ConvertTo-Json -Depth 8
    $routed=Invoke-RestMethod -Method Post -Uri ($rabbitBase+'/exchanges/%2F/asset.tasks/publish') -Headers $rabbitHeaders -ContentType 'application/json' -Body $publish
    if (-not $routed.routed) { throw 'Fault injection was not routed' }
    $deadline=(Get-Date).AddSeconds(15);$retained=$false
    do {
        $queue=Invoke-RestMethod -Uri ($rabbitBase+'/queues/%2F/task-report-worker') -Headers $rabbitHeaders
        if ($queue.messages -ge 1) { $retained=$true;break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    if (-not $retained) { throw 'Message was acknowledged while quarantine database was offline' }
    Compose -Arguments @('start','postgres') | Out-Null; $postgresStopped=$false
    $deadline=(Get-Date).AddSeconds(90)
    do {
        try { $quarantined=Sql "SELECT count(*) FROM task_report.message_quarantine WHERE body='$poison';" } catch { $quarantined='0' }
        if ($quarantined -eq '1') { break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    if ($quarantined -ne '1') { throw 'Message was not durably quarantined after PostgreSQL returned' }
    Write-Output 'PostgreSQL outage passed: delivery_retained=true quarantine_after_restart=1'

    # Unavailable object storage is transient; consume the full retry budget.
    Compose -Arguments @('stop','minio') | Out-Null; $minioStopped=$true
    $temporary=Create-Export
    $exhausted=Wait-Task $temporary.id 'dead_letter'
    if ($exhausted.attempt -ne 3) { throw "Transient MinIO failure attempt=$($exhausted.attempt), expected 3" }
    $deadEvents=Sql "SELECT count(*) FROM task_report.outbox WHERE aggregate_id='$($temporary.id)' AND topic='task.dead';"
    if ($deadEvents -ne '1') { throw 'Transient failure did not durably record exactly one dead outbox event' }
    Compose -Arguments @('start','minio') | Out-Null; $minioStopped=$false
    Write-Output 'MinIO outage passed: transient_attempts=3 dead_outbox=1'

    # The 100k capacity fixture makes this export long enough to kill after Claim.
    $long=Create-Export
    $running=Wait-Task $long.id 'running' 20
    if ($running.attempt -ne 1) { throw 'Expected first execution before SIGKILL' }
    Compose -Arguments @('kill','--signal','SIGKILL','task-worker') | Out-Null
    Compose -Arguments @('start','task-worker') | Out-Null
    $recovered=Wait-Task $long.id 'succeeded' 180
    if ($recovered.attempt -ne 2) { throw "SIGKILL task attempt=$($recovered.attempt), expected 2" }
    $generation=Sql "SELECT dispatch_version FROM task_report.tasks WHERE id='$($long.id)';"
    if ($generation -ne '2') { throw 'SIGKILL did not advance exactly one dispatch generation' }
    if ($recovered.result.object_key -notmatch ('^exports/'+[regex]::Escape($long.id)+'/[0-9a-f-]{36}\.csv$')) { throw 'Recovered export did not isolate its object by token' }
    Write-Output "SIGKILL recovery passed: task=$($long.id) attempts=2 dispatch_version=2 rows=$($recovered.result.rows)"
}
finally {
    if ($rabbitStopped) { & docker @composeArgs start rabbitmq | Out-Null }
    if ($postgresStopped) { & docker @composeArgs start postgres | Out-Null }
    if ($minioStopped) { & docker @composeArgs start minio | Out-Null }
}

