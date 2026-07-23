#!/usr/bin/env sh
set -eu

export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 24)}"
export RABBITMQ_PASSWORD="${RABBITMQ_PASSWORD:-$(openssl rand -hex 24)}"
export MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-$(openssl rand -hex 24)}"

cleanup() {
  docker compose logs --no-color > compose-smoke.log 2>&1 || true
  docker compose down || true
}
trap cleanup EXIT

docker compose config --quiet
docker compose up --build --detach --wait --wait-timeout 240

curl --fail --silent http://127.0.0.1:${GATEWAY_PORT:-8080}/health/ready >/dev/null
curl --fail --silent http://127.0.0.1:${WEB_PORT:-8088}/ >/dev/null

docker compose exec -T iam-service wget -qO- http://127.0.0.1:8081/health/ready >/dev/null
docker compose exec -T asset-service wget -qO- http://127.0.0.1:8082/health/ready >/dev/null
docker compose exec -T governance-service wget -qO- http://127.0.0.1:8083/health/ready >/dev/null
docker compose exec -T task-report-service wget -qO- http://127.0.0.1:8084/health/ready >/dev/null

echo "Compose integration smoke test passed"
