$ErrorActionPreference = 'Stop'

$env:COMPOSE_PROJECT_NAME = "asset-governance-smoke-$([guid]::NewGuid().ToString('N'))"
if (-not $env:POSTGRES_PASSWORD) { $env:POSTGRES_PASSWORD = [guid]::NewGuid().ToString('N') }
if (-not $env:RABBITMQ_PASSWORD) { $env:RABBITMQ_PASSWORD = [guid]::NewGuid().ToString('N') }
if (-not $env:MINIO_ROOT_PASSWORD) { $env:MINIO_ROOT_PASSWORD = [guid]::NewGuid().ToString('N') }
if (-not $env:IAM_BOOTSTRAP_ADMIN_PASSWORD) { $env:IAM_BOOTSTRAP_ADMIN_PASSWORD = [guid]::NewGuid().ToString('N') }

try {
    docker compose config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Compose configuration is invalid' }
    docker compose up --build --detach --wait --wait-timeout 240
    if ($LASTEXITCODE -ne 0) { throw 'Compose environment failed to become healthy' }

    $gatewayPort = if ($env:GATEWAY_PORT) { $env:GATEWAY_PORT } else { '8080' }
    $webPort = if ($env:WEB_PORT) { $env:WEB_PORT } else { '8088' }
    Invoke-RestMethod -Uri "http://127.0.0.1:$gatewayPort/health/ready" | Out-Null
    Invoke-WebRequest -Uri "http://127.0.0.1:$webPort/" -UseBasicParsing | Out-Null

    foreach ($service in @(
        @{ Name = 'iam-service'; Port = 8081 },
        @{ Name = 'asset-service'; Port = 8082 },
        @{ Name = 'governance-service'; Port = 8083 },
        @{ Name = 'task-report-service'; Port = 8084 }
    )) {
        docker compose exec -T $service.Name wget -qO- "http://127.0.0.1:$($service.Port)/health/ready" | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "$($service.Name) health check failed" }
    }

    $gateway = "http://127.0.0.1:$gatewayPort"
    $webSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $loginBody = @{
        username = 'admin'
        password = $env:IAM_BOOTSTRAP_ADMIN_PASSWORD
    } | ConvertTo-Json
    $login = Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/auth/login" `
        -ContentType 'application/json' -Body $loginBody -WebSession $webSession
    if (-not $login.access_token -or $login.expires_in -ne 900) {
        throw 'IAM login did not issue a 15-minute access token'
    }
    if (-not $login.user.must_change_password) {
        throw 'Bootstrap administrator was not required to change the initial password'
    }
    $headers = @{ Authorization = "Bearer $($login.access_token)" }
    $changedPassword = "$([guid]::NewGuid().ToString('N'))aA1!"
    $changeBody = @{
        current_password = $env:IAM_BOOTSTRAP_ADMIN_PASSWORD
        new_password = $changedPassword
    } | ConvertTo-Json
    Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/auth/change-password" `
        -Headers $headers -ContentType 'application/json' -Body $changeBody -WebSession $webSession
    $webSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $loginBody = @{ username = 'admin'; password = $changedPassword } | ConvertTo-Json
    $login = Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/auth/login" `
        -ContentType 'application/json' -Body $loginBody -WebSession $webSession
    if ($login.user.must_change_password) {
        throw 'Password change did not clear the bootstrap password flag'
    }
    $headers = @{ Authorization = "Bearer $($login.access_token)" }
    $types = Invoke-RestMethod -Uri "$gateway/api/v1/asset-types" -Headers $headers
    if ($types.asset_types.Count -ne 7) { throw 'Expected seven generated asset types' }

    $createBody = @{
        fields = @{ department_name = "Compose smoke $($env:COMPOSE_PROJECT_NAME)" }
    } | ConvertTo-Json -Depth 5
    $created = Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/assets/responsible-department" `
        -Headers $headers -ContentType 'application/json' -Body $createBody
    if ($created.asset.version -ne 1) { throw 'Asset create did not start at version 1' }
    $assetId = $created.asset.id

    $updateBody = @{
        version = 1
        fields = @{ department_code = 'SMOKE' }
    } | ConvertTo-Json -Depth 5
    $updated = Invoke-RestMethod -Method Patch `
        -Uri "$gateway/api/v1/assets/responsible-department/$assetId" `
        -Headers $headers -ContentType 'application/json' -Body $updateBody
    if ($updated.asset.version -ne 2) { throw 'Asset update did not increment the version' }
    $versions = Invoke-RestMethod `
        -Uri "$gateway/api/v1/assets/responsible-department/$assetId/versions" -Headers $headers
    if ($versions.versions.Count -ne 2) { throw 'Asset version history is incomplete' }

    $refresh = Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/auth/refresh" -WebSession $webSession
    if (-not $refresh.access_token -or $refresh.access_token -eq $login.access_token) {
        throw 'Refresh Token rotation did not issue a new Access Token'
    }
    $headers = @{ Authorization = "Bearer $($refresh.access_token)" }
    Invoke-RestMethod -Method Delete `
        -Uri "$gateway/api/v1/assets/responsible-department/$assetId`?version=2" -Headers $headers
    $deleted = Invoke-RestMethod `
        -Uri "$gateway/api/v1/assets/responsible-department/$assetId`?include_deleted=true" -Headers $headers
    if (-not $deleted.asset.deleted_at -or $deleted.asset.version -ne 3) {
        throw 'Asset soft delete did not preserve a recoverable version'
    }
    $restoreBody = @{ version = 3 } | ConvertTo-Json
    $restored = Invoke-RestMethod -Method Post `
        -Uri "$gateway/api/v1/assets/responsible-department/$assetId/restore" `
        -Headers $headers -ContentType 'application/json' -Body $restoreBody
    if ($restored.asset.deleted_at -or $restored.asset.version -ne 4) {
        throw 'Asset restore did not increment the version'
    }
    Invoke-RestMethod -Method Post -Uri "$gateway/api/v1/auth/logout" -WebSession $webSession
    try {
        Invoke-WebRequest -Method Post -Uri "$gateway/api/v1/auth/refresh" `
            -WebSession $webSession -UseBasicParsing | Out-Null
        throw 'Refresh succeeded after logout'
    }
    catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 401) { throw }
    }
    Write-Output 'Compose integration smoke test passed'
}
finally {
    docker compose logs --no-color *> compose-smoke.log
    docker compose down --volumes --remove-orphans
}
