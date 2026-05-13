#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Make testcontainers + compose talk to rootless podman.
export DOCKER_HOST="${DOCKER_HOST:-unix:///run/user/$(id -u)/podman/podman.sock}"
export TESTCONTAINERS_RYUK_DISABLED=true

cleanup() { podman compose --profile baseline down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

cleanup
podman compose --profile baseline up -d --build postgres
podman compose --profile baseline run --rm migrate
podman compose --profile baseline up -d --build vote-api tally-worker results-api

# wait for vote-api readiness
for i in $(seq 1 30); do
  if curl -fsS http://localhost:8081/readyz >/dev/null 2>&1; then break; fi
  sleep 1
done

# 5 tacos, 2 burritos
for i in 1 2 3 4 5; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

# wait for at least one tally interval + slack
sleep 8

OUT="$(curl -fsS 'http://localhost:8082/results?poll_id=smoke')"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }
echo "smoke OK"
