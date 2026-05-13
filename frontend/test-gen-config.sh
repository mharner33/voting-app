#!/bin/sh
# Test gen-config.sh by invoking it with various env vars and
# comparing the stdout JSON line-for-line with expected output.

set -eu
cd "$(dirname "$0")"

fail=0
run_case() {
  desc="$1"; expected="$2"; shift 2
  # Run with a clean env: unset any leaked VOTING_* values first,
  # then export only what this case sets, then run.
  actual=$(env -i PATH="$PATH" "$@" ./gen-config.sh)
  if [ "$actual" = "$expected" ]; then
    echo "PASS: $desc"
  else
    echo "FAIL: $desc"
    echo "  expected: $expected"
    echo "  actual:   $actual"
    fail=1
  fi
}

run_case "defaults" \
  '{"choices":["tacos","burritos"],"poll_id":"default","heading":"Default poll: choose one."}'

run_case "override choices" \
  '{"choices":["pizza","salad","sushi"],"poll_id":"default","heading":"Default poll: choose one."}' \
  VOTING_CHOICES=pizza,salad,sushi

run_case "override poll_id and heading" \
  '{"choices":["tacos","burritos"],"poll_id":"lunch","heading":"What for lunch?"}' \
  VOTING_POLL_ID=lunch VOTING_HEADING="What for lunch?"

run_case "single choice" \
  '{"choices":["onlyone"],"poll_id":"default","heading":"Default poll: choose one."}' \
  VOTING_CHOICES=onlyone

exit $fail
