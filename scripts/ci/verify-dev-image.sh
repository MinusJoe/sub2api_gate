#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: verify-dev-image.sh image@sha256:digest}"
: "${GITHUB_REPOSITORY:?}"
[[ "$image" == "ghcr.io/${GITHUB_REPOSITORY,,}@"* && "${image#*@}" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo 'Expected the immutable sub2api_gate image digest' >&2
  exit 1
}
: "${VERSION:?}" "${GITHUB_SHA:?}"
prefix="sub2api-smoke-${GITHUB_RUN_ID:-local}-$$"
network="$prefix-net"
postgres="$prefix-postgres"
redis="$prefix-redis"
log_dir="${RUNNER_TEMP:-/tmp}/$prefix"
mkdir -p "$log_dir"
containers=()

cleanup() {
  status=$?
  trap - EXIT
  set +e
  for container in "${containers[@]}"; do
    docker inspect "$container" >/dev/null 2>&1 || continue
    docker logs "$container" > "$log_dir/$container.log" 2>&1
    if [[ "$status" != 0 ]]; then tail -n 100 "$log_dir/$container.log"; fi
    docker rm -fv "$container" >/dev/null 2>&1
  done
  docker network rm "$network" >/dev/null 2>&1
  exit "$status"
}
trap cleanup EXIT

docker buildx imagetools inspect "$image" --raw > "$log_dir/manifest.json"

db_password="$(openssl rand -hex 24)"
admin_password="$(openssl rand -hex 24)"
jwt_secret="$(openssl rand -hex 32)"
docker network create "$network" >/dev/null
containers+=("$postgres")
docker run -d --name "$postgres" --network "$network" \
  -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD="$db_password" \
  postgres:18-alpine >/dev/null
containers+=("$redis")
docker run -d --name "$redis" --network "$network" redis:8.4-alpine >/dev/null

ready=false
for ((attempt=0; attempt<60; attempt++)); do
  if docker exec "$postgres" pg_isready -U sub2api >/dev/null 2>&1 && \
    [[ "$(docker exec "$redis" redis-cli ping 2>/dev/null)" == PONG ]]; then
    ready=true
    break
  fi
  sleep 2
done
[[ "$ready" == true ]] || { echo 'Smoke-test services did not become ready' >&2; exit 1; }

architectures=(amd64 arm64)
for index in "${!architectures[@]}"; do
  arch="${architectures[$index]}"
  platform="linux/$arch"
  container="$prefix-$arch"
  database="smoke_$arch"
  platform_digest="$(jq -er --arg arch "$arch" '
    [.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch)]
    | if length == 1 then .[0].digest else error("Expected one image for the requested architecture") end
  ' "$log_dir/manifest.json")"
  [[ "$platform_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || exit 1
  platform_image="${image%@*}@$platform_digest"
  echo "Verifying $platform: $platform_image (index: $image)"
  docker run --rm --platform "$platform" "$platform_image" --version 2>&1 | tee "$log_dir/$arch-version.txt"
  grep -F "$VERSION" "$log_dir/$arch-version.txt"
  grep -F "$GITHUB_SHA" "$log_dir/$arch-version.txt"
  docker run --rm --platform "$platform" "$platform_image" /bin/sh -ec \
    'pg_dump --version; psql --version; test -s /app/resources/model-pricing/model_prices_and_context_window.json'

  docker exec "$postgres" psql -U sub2api -d postgres -v ON_ERROR_STOP=1 \
    -c "CREATE DATABASE $database" >/dev/null
  containers+=("$container")
  docker run -d --name "$container" --platform "$platform" --network "$network" \
    -p 127.0.0.1::8080 --tmpfs /app/data:rw,size=128m \
    -e AUTO_SETUP=true -e SERVER_HOST=0.0.0.0 -e SERVER_PORT=8080 -e SERVER_MODE=release \
    -e DATABASE_HOST="$postgres" -e DATABASE_PORT=5432 -e DATABASE_USER=sub2api \
    -e DATABASE_PASSWORD="$db_password" -e DATABASE_DBNAME="$database" -e DATABASE_SSLMODE=disable \
    -e REDIS_HOST="$redis" -e REDIS_PORT=6379 -e REDIS_DB="$index" \
    -e ADMIN_EMAIL=ci-smoke@example.invalid -e ADMIN_PASSWORD="$admin_password" \
    -e JWT_SECRET="$jwt_secret" -e TZ=UTC "$platform_image" >/dev/null

  address="$(docker port "$container" 8080/tcp | head -n 1)"
  ready=false
  for ((attempt=0; attempt<120; attempt++)); do
    if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" != true ]]; then
      echo "$platform container exited during startup" >&2
      exit 1
    fi
    if curl -fsS --max-time 3 "http://$address/health" -o "$log_dir/$arch-health.json" 2>/dev/null && \
      jq -e '.status == "ok"' "$log_dir/$arch-health.json" >/dev/null && \
      [[ "$(docker inspect --format '{{.State.Health.Status}}' "$container")" == healthy ]]; then
      ready=true
      break
    fi
    sleep 2
  done
  [[ "$ready" == true ]] || { echo "$platform did not become healthy" >&2; exit 1; }
  curl -fsS --max-time 10 "http://$address/setup/status" | \
    jq -e '.code == 0 and .data.needs_setup == false' >/dev/null
  curl -fsS --max-time 10 "http://$address/api/v1/settings/public" | \
    jq -e '.code == 0 and .data.sidebar_menu_visibility != null' >/dev/null
  curl -fsS --max-time 10 "http://$address/" -o "$log_dir/$arch-index.html"
  grep -F 'id="app"' "$log_dir/$arch-index.html" >/dev/null
  asset="$(python3 - "$log_dir/$arch-index.html" <<'PY'
from pathlib import Path
import re
import sys

html = Path(sys.argv[1]).read_text()
match = re.search(r'src="(/assets/[^" ]+\.js)"', html)
if not match:
    raise SystemExit('Embedded frontend JavaScript asset is missing')
print(match.group(1))
PY
  )"
  curl -fsS --max-time 10 "http://$address$asset" -o /dev/null
  echo "$platform startup, Docker healthcheck, setup, settings and frontend: passed"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf -- '- %s: startup, healthcheck, database migration and frontend passed\n' "$platform" >> "$GITHUB_STEP_SUMMARY"
  fi
  docker logs "$container" > "$log_dir/$arch-startup.log" 2>&1
  docker rm -fv "$container" >/dev/null
done
