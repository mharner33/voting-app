#!/bin/sh
# Emit the voting-app frontend's runtime config as a single line of JSON
# on stdout. Reads VOTING_CHOICES (comma-separated), VOTING_POLL_ID, and
# VOTING_HEADING from the environment; falls back to demo defaults.
#
# Limitations: individual choices must not contain commas, double quotes,
# or backslashes. See .env.example.

set -eu

: "${VOTING_CHOICES:=tacos,burritos}"
: "${VOTING_POLL_ID:=default}"
: "${VOTING_HEADING:=Default poll: choose one.}"

choices_json=""
old_ifs=$IFS
IFS=','
for c in $VOTING_CHOICES; do
  if [ -n "$choices_json" ]; then
    choices_json="$choices_json,"
  fi
  choices_json="$choices_json\"$c\""
done
IFS=$old_ifs

printf '{"choices":[%s],"poll_id":"%s","heading":"%s"}' \
  "$choices_json" "$VOTING_POLL_ID" "$VOTING_HEADING"
