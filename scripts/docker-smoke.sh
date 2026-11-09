#!/usr/bin/env bash
# Builds the production image, starts it in demo mode, and checks health,
# the UI, the API, image size, and idle memory. Cleans up on exit.
set -euo pipefail
cd "$(dirname "$0")/.."

project=evcc-planner-smoke
port=${SMOKE_PORT:-7204}
compose=(docker compose -p "$project" -f compose.dev.yaml)

cleanup() { DEV_PORT=$port "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

command -v docker >/dev/null || { echo "Docker is required for docker-smoke"; exit 1; }

# On macOS, trust the keychain CAs during the build (TLS-inspecting networks).
if [ "$(uname)" = "Darwin" ] && [ -z "${BUILD_CA_FILE:-}" ]; then
  BUILD_CA_FILE=$(mktemp)
  security find-certificate -a -p /Library/Keychains/System.keychain \
    /System/Library/Keychains/SystemRootCertificates.keychain > "$BUILD_CA_FILE"
  export BUILD_CA_FILE
  trap 'cleanup; rm -f "$BUILD_CA_FILE"' EXIT
fi

# Use the same npm registry as the host (e.g. a mirror on restricted networks).
if [ -z "${NPM_REGISTRY:-}" ] && command -v npm >/dev/null; then
  NPM_REGISTRY=$(npm config get registry)
  export NPM_REGISTRY
fi

echo "==> Building image"
DEV_PORT=$port "${compose[@]}" build
echo "==> Starting container on 127.0.0.1:$port"
DEV_PORT=$port EVCC_PLANNER_DEMO=true "${compose[@]}" up -d

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null || {
  echo "Container did not become ready; logs:"; DEV_PORT=$port "${compose[@]}" logs; exit 1; }

echo "==> Checking UI and API"
curl -fsS "http://127.0.0.1:$port/" | grep -q "<title>EVCC Trip Planner</title>" || { echo "UI not served"; exit 1; }
curl -fsS "http://127.0.0.1:$port/trips" | grep -q 'id="app"' || { echo "SPA fallback failed"; exit 1; }
curl -fsS "http://127.0.0.1:$port/api/plan" | grep -q '"days"' || { echo "plan API failed"; exit 1; }

container=$(DEV_PORT=$port "${compose[@]}" ps -q evcc-planner)
for _ in $(seq 1 30); do
  health=$(docker inspect -f '{{.State.Health.Status}}' "$container")
  [ "$health" = "healthy" ] && break
  sleep 1
done
echo "Container health: $health"
[ "$health" = "healthy" ] || { echo "Healthcheck did not pass"; exit 1; }

sleep 3
size=$(docker image ls --format '{{.Size}}' evcc-planner:dev)
mem=$(docker stats --no-stream --format '{{.MemUsage}}' "$container")
echo "Image size (unpacked): $size"
echo "Idle memory: $mem"
echo "Docker smoke test passed."
