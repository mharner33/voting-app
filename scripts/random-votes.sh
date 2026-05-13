#!/usr/bin/env bash
#
# Generate a steady trickle of random votes against vote-api for the demo.
# Ctrl+C to stop.
#
# Tunables (env vars):
#   VOTE_URL      — default http://localhost:8081/vote
#   POLL_ID       — default "default"
#   CHOICES       — space-separated; default "tacos burritos pizza salad"
#   INTERVAL      — seconds between votes; default 5

set -euo pipefail

VOTE_URL="${VOTE_URL:-http://localhost:8081/vote}"
POLL_ID="${POLL_ID:-default}"
CHOICES="${CHOICES:-tacos burritos pizza salad}"
INTERVAL="${INTERVAL:-5}"

read -ra choice_arr <<<"$CHOICES"

echo "Posting random votes to $VOTE_URL (poll_id=$POLL_ID, interval=${INTERVAL}s)"
echo "Choices: ${choice_arr[*]}"
echo "Ctrl+C to stop."
echo

while true; do
  choice="${choice_arr[RANDOM % ${#choice_arr[@]}]}"
  user_id="u$(( RANDOM % 1000 ))"
  ts=$(date +'%H:%M:%S')

  if curl -fsS -X POST "$VOTE_URL" \
       -H 'content-type: application/json' \
       -d "{\"poll_id\":\"$POLL_ID\",\"choice\":\"$choice\",\"user_id\":\"$user_id\"}" \
       >/dev/null 2>&1; then
    echo "$ts  ✓  $choice  user=$user_id"
  else
    echo "$ts  ✗  $choice  user=$user_id  (vote-api unreachable?)"
  fi

  sleep "$INTERVAL"
done
