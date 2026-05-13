#!/bin/sh
# Generate /usr/share/nginx/html/config.json from VOTING_* env vars,
# then hand off to nginx. Runs once per container start.

set -eu

/gen-config.sh > /usr/share/nginx/html/config.json

exec nginx -g 'daemon off;'
