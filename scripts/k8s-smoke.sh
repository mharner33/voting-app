#!/usr/bin/env bash
# End-to-end smoke against a deployed k8s overlay.
# Requires: kubectl context already pointing at the cluster, manifests applied,
# Job 'migrate' complete.

set -euo pipefail

OVERLAY="${OVERLAY:-dev}"
NAMESPACE="${NAMESPACE:-voting-app}"
HOST="${HOST:?HOST must be set to the Ingress host, e.g. voting-dev.example.com}"
SCHEME="${SCHEME:-http}"
POLL_ID="${POLL_ID:-smoke}"
TALLY_WAIT="${TALLY_WAIT:-12}"   # 2x default TALLY_INTERVAL + slack

echo "==> Smoke against ${SCHEME}://${HOST} (overlay=${OVERLAY}, ns=${NAMESPACE})"

echo "==> Waiting for migrate Job"
kubectl -n "$NAMESPACE" wait --for=condition=complete --timeout=120s job/migrate

echo "==> Waiting for Deployments"
for d in vote-api results-api tally-worker frontend; do
  kubectl -n "$NAMESPACE" wait --for=condition=available --timeout=120s "deployment/$d"
done

echo "==> Casting votes"
for i in 1 2 3 4 5; do
  curl -fsS -X POST "${SCHEME}://${HOST}/vote" \
    -H 'content-type: application/json' \
    -d "{\"poll_id\":\"${POLL_ID}\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST "${SCHEME}://${HOST}/vote" \
    -H 'content-type: application/json' \
    -d "{\"poll_id\":\"${POLL_ID}\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

echo "==> Sleeping ${TALLY_WAIT}s for tally-worker"
sleep "$TALLY_WAIT"

echo "==> Fetching results"
OUT="$(curl -fsS "${SCHEME}://${HOST}/results?poll_id=${POLL_ID}")"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }

echo "==> PASS"
