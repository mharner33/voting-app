#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

export DOCKER_HOST="${DOCKER_HOST:-unix:///run/user/$(id -u)/podman/podman.sock}"
export TESTCONTAINERS_RYUK_DISABLED=true

cleanup() { podman compose --profile temporal down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

cleanup
podman compose --profile temporal up -d --build postgres
podman compose --profile temporal run --rm migrate
podman compose --profile temporal up -d --build temporal
podman compose --profile temporal up -d --build vote-api results-api temporal-ui tally-worker-temporal

# Wait for vote-api readiness.
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8081/readyz >/dev/null 2>&1; then break; fi
  sleep 1
done

# Wait for the Temporal Schedule "tally-all" to exist. The worker creates
# it on startup; failing this check means the worker never connected.
for i in $(seq 1 60); do
  if podman compose --profile temporal exec -T temporal \
       tctl --address temporal:7233 schedule list 2>/dev/null | grep -q '^tally-all'; then
    break
  fi
  sleep 1
done
podman compose --profile temporal exec -T temporal \
  tctl --address temporal:7233 schedule list | grep -q '^tally-all' \
  || { echo "FAIL: tally-all Schedule was not created"; exit 1; }

# 5 tacos, 2 burritos.
for i in 1 2 3 4 5; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke-temporal\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke-temporal\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

# Wait for at least one tally interval + Temporal scheduling slack.
sleep 10

OUT="$(curl -fsS 'http://localhost:8082/results?poll_id=smoke-temporal')"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }
echo "smoke-temporal OK"
