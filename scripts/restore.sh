#!/usr/bin/env bash
# Restores a database from a backup made by backup.sh.
#
# Usage:
#   DATABASE_URL=postgres://... ./scripts/restore.sh backups/aisp-20260101T000000Z.sql.gz [--force]
#
# This REPLACES the contents of the target database. By default it asks for
# confirmation by typing the database name; pass --force (e.g. for a
# scripted disaster-recovery drill) to skip that prompt.
#
# The restored data is only as useful as the SECRETS_KEY it was encrypted
# with: restoring into an environment with a different SECRETS_KEY leaves
# every stored customer credential permanently unreadable (see backup.sh).
set -euo pipefail

file="${1:-}"
force="${2:-}"
if [ -z "$file" ] || [ ! -f "$file" ]; then
  echo "usage: DATABASE_URL=... $0 <backup-file.sql.gz> [--force]" >&2
  exit 1
fi
if [ -z "${DATABASE_URL:-}" ]; then
  echo "error: DATABASE_URL is not set" >&2
  exit 1
fi
for cmd in psql gunzip; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: $cmd not found (install the postgresql client tools)" >&2
    exit 1
  fi
done

dbname="$(psql "$DATABASE_URL" -tAc 'select current_database()')"
echo "target: $dbname (from DATABASE_URL)"
echo "source: $file"

# A psql/pg_dump newer than the server can emit session settings (e.g.
# transaction_timeout, added in PG17) the server doesn't recognize, aborting
# the restore partway through. Use client tools matching the server's major
# version — e.g. the same postgres:16-alpine image used to run it.
client_major="$(psql --version | grep -oE '[0-9]+' | head -1)"
server_major="$(psql "$DATABASE_URL" -tAc 'show server_version_num' | cut -c1-2)"
if [ -n "$client_major" ] && [ -n "$server_major" ] && [ "$client_major" -gt "$server_major" ]; then
  echo "warning: your psql ($client_major.x) is newer than the server (${server_major}.x)." >&2
  echo "         If the restore fails with 'unrecognized configuration parameter', use psql/pg_dump matching the server's version." >&2
fi

if [ "$force" != "--force" ]; then
  read -r -p "This REPLACES every table in '$dbname'. Type the database name to continue: " confirm
  if [ "$confirm" != "$dbname" ]; then
    echo "aborted: confirmation did not match"
    exit 1
  fi
fi

echo "restoring ..."
gunzip -c "$file" | psql "$DATABASE_URL" --set ON_ERROR_STOP=on -q
echo "done. Verify with: psql \"\$DATABASE_URL\" -c 'select count(*) from orgs;'"
