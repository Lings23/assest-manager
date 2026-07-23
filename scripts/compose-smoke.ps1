$ErrorActionPreference = 'Stop'

if (-not $env:POSTGRES_PASSWORD) { $env:POSTGRES_PASSWORD = [guid]::NewGuid().ToString('N') }
if (-not $env:RABBITMQ_PASSWORD) { $env:RABBITMQ_PASSWORD = [guid]::NewGuid().ToString('N') }
if (-not $env:MINIO_ROOT_PASSWORD) { $env:MINIO_ROOT_PASSWORD = [guid]::NewGuid().ToString('N') }

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
    Write-Output 'Compose integration smoke test passed'
}
finally {
    docker compose logs --no-color *> compose-smoke.log
    docker compose down
}
