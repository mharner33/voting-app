#!/bin/bash
# Creates the Datadog monitoring role on first init of the postgres data
# volume. The postgres image runs anything in /docker-entrypoint-initdb.d/
# exactly once, when initdb runs against an empty data directory.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
  CREATE USER ${DD_POSTGRES_USER} WITH PASSWORD '${DD_POSTGRES_PASSWORD}';
  GRANT pg_monitor TO ${DD_POSTGRES_USER};
  GRANT SELECT ON pg_stat_database TO ${DD_POSTGRES_USER};
SQL
