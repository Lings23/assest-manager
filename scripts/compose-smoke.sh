#!/usr/bin/env sh
set -eu

export COMPOSE_PROJECT_NAME="asset-governance-smoke-$$"
export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 24)}"
export RABBITMQ_PASSWORD="${RABBITMQ_PASSWORD:-$(openssl rand -hex 24)}"
export MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-$(openssl rand -hex 24)}"
export IAM_BOOTSTRAP_ADMIN_PASSWORD="${IAM_BOOTSTRAP_ADMIN_PASSWORD:-$(openssl rand -hex 24)}"
cookie_jar="$(mktemp)"

cleanup() {
  docker compose logs --no-color > compose-smoke.log 2>&1 || true
  docker compose down --volumes --remove-orphans || true
  rm -f "$cookie_jar"
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

gateway="http://127.0.0.1:${GATEWAY_PORT:-8080}"
login_payload="$(node -e 'process.stdout.write(JSON.stringify({username:"admin",password:process.env.IAM_BOOTSTRAP_ADMIN_PASSWORD}))')"
login_json="$(curl --fail --silent --cookie-jar "$cookie_jar" \
  -H 'Content-Type: application/json' -d "$login_payload" "$gateway/api/v1/auth/login")"
access_token="$(printf '%s' "$login_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(JSON.parse(d).access_token||""))')"
test -n "$access_token"
auth_header="Authorization: Bearer $access_token"
must_change="$(printf '%s' "$login_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).user.must_change_password)))')"
test "$must_change" = "true"

changed_password="$(openssl rand -hex 24)Aa1!"
export SMOKE_CHANGED_PASSWORD="$changed_password"
change_payload="$(node -e 'process.stdout.write(JSON.stringify({current_password:process.env.IAM_BOOTSTRAP_ADMIN_PASSWORD,new_password:process.env.SMOKE_CHANGED_PASSWORD}))')"
curl --fail --silent -X POST -H "$auth_header" -H 'Content-Type: application/json' \
  -d "$change_payload" "$gateway/api/v1/auth/change-password" >/dev/null
login_payload="$(node -e 'process.stdout.write(JSON.stringify({username:"admin",password:process.env.SMOKE_CHANGED_PASSWORD}))')"
login_json="$(curl --fail --silent --cookie-jar "$cookie_jar" \
  -H 'Content-Type: application/json' -d "$login_payload" "$gateway/api/v1/auth/login")"
access_token="$(printf '%s' "$login_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(JSON.parse(d).access_token||""))')"
must_change="$(printf '%s' "$login_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).user.must_change_password)))')"
test -n "$access_token"
test "$must_change" = "false"
auth_header="Authorization: Bearer $access_token"

types_json="$(curl --fail --silent -H "$auth_header" "$gateway/api/v1/asset-types")"
type_count="$(printf '%s' "$types_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).asset_types.length)))')"
test "$type_count" = "7"

created_json="$(curl --fail --silent -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"fields":{"department_name":"Compose smoke department"}}' \
  "$gateway/api/v1/assets/responsible-department")"
asset_id="$(printf '%s' "$created_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(JSON.parse(d).asset.id))')"
test -n "$asset_id"

updated_json="$(curl --fail --silent -X PATCH -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"version":1,"fields":{"department_code":"SMOKE"}}' \
  "$gateway/api/v1/assets/responsible-department/$asset_id")"
updated_version="$(printf '%s' "$updated_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).asset.version)))')"
test "$updated_version" = "2"

versions_json="$(curl --fail --silent -H "$auth_header" \
  "$gateway/api/v1/assets/responsible-department/$asset_id/versions")"
version_count="$(printf '%s' "$versions_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).versions.length)))')"
test "$version_count" = "2"

refresh_json="$(curl --fail --silent --cookie "$cookie_jar" --cookie-jar "$cookie_jar" \
  -X POST "$gateway/api/v1/auth/refresh")"
rotated_access_token="$(printf '%s' "$refresh_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(JSON.parse(d).access_token||""))')"
test -n "$rotated_access_token"
test "$rotated_access_token" != "$access_token"
auth_header="Authorization: Bearer $rotated_access_token"

curl --fail --silent -X DELETE -H "$auth_header" \
  "$gateway/api/v1/assets/responsible-department/$asset_id?version=2" >/dev/null
deleted_json="$(curl --fail --silent -H "$auth_header" \
  "$gateway/api/v1/assets/responsible-department/$asset_id?include_deleted=true")"
deleted_version="$(printf '%s' "$deleted_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).asset.version)))')"
test "$deleted_version" = "3"

restored_json="$(curl --fail --silent -X POST -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"version":3}' "$gateway/api/v1/assets/responsible-department/$asset_id/restore")"
restored_version="$(printf '%s' "$restored_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>process.stdout.write(String(JSON.parse(d).asset.version)))')"
test "$restored_version" = "4"

curl --fail --silent --cookie "$cookie_jar" -X POST "$gateway/api/v1/auth/logout" >/dev/null
status="$(curl --silent --output /dev/null --write-out '%{http_code}' --cookie "$cookie_jar" \
  -X POST "$gateway/api/v1/auth/refresh")"
test "$status" = "401"

echo "Compose integration smoke test passed"
