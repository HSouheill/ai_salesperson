#!/usr/bin/env bash
# Dumps the database to a timestamped, gzip-compressed SQL file.
#
# Usage:
#   DATABASE_URL=postgres://... ./scripts/backup.sh [dir]
#
# [dir] defaults to ./backups. With AWS_S3_BACKUP_BUCKET set (and the aws CLI
# installed), the dump is also uploaded there — this is optional and skipped
# silently if either is missing, so the script still works for a simple local
# backup. Local backups older than BACKUP_RETENTION_DAYS (default 14) are
# pruned after a successful run.
#
# This backs up the database only. SECRETS_KEY is what makes the encrypted
# credentials in it readable — back that up separately (a password manager or
# secrets vault, never alongside the dump) and keep it forever: a database
# restored without the matching SECRETS_KEY has permanently unreadable
# customer SMTP/IMAP/API credentials, not just locked ones.
set -euo pipefail

if [ -z "${DATABASE_URL:-}" ]; then
  echo "error: DATABASE_URL is not set" >&2
  exit 1
fi
if ! command -v pg_dump >/dev/null 2>&1; then
  echo "error: pg_dump not found (install the postgresql client tools)" >&2
  exit 1
fi

dir="${1:-./backups}"
mkdir -p "$dir"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
file="$dir/aisp-$stamp.sql.gz"
tmp="$file.part"

echo "backing up to $file ..."
pg_dump "$DATABASE_URL" --no-owner --no-privileges | gzip -9 > "$tmp"
mv "$tmp" "$file"
size=$(du -h "$file" | cut -f1)
echo "done: $file ($size)"

if [ -n "${AWS_S3_BACKUP_BUCKET:-}" ] && command -v aws >/dev/null 2>&1; then
  echo "uploading to s3://$AWS_S3_BACKUP_BUCKET/$(basename "$file") ..."
  aws s3 cp "$file" "s3://$AWS_S3_BACKUP_BUCKET/$(basename "$file")"
fi

keep_days="${BACKUP_RETENTION_DAYS:-14}"
find "$dir" -name 'aisp-*.sql.gz' -mtime "+$keep_days" -print -delete || true
echo "retention: kept backups from the last $keep_days day(s)"
