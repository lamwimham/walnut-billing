#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/sqlite.sh
source "$ROOT_DIR/scripts/lib/sqlite.sh"

usage() {
  cat <<USAGE
Usage: scripts/backup_sqlite.sh [database_dsn_or_path] [backup_dir]

Environment:
  DATABASE_DSN                 Default source when first arg is omitted.
  WALNUT_BILLING_BACKUP_DIR    Default backup directory when second arg is omitted.
  WALNUT_BILLING_BACKUP_PREFIX Backup filename prefix, default walnut_billing.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_sqlite3
cd "$ROOT_DIR"

DB_DSN="${1:-${DATABASE_DSN:-./walnut_billing.db}}"
BACKUP_DIR="${2:-${WALNUT_BILLING_BACKUP_DIR:-$ROOT_DIR/.tmp/backups}}"
BACKUP_PREFIX="${WALNUT_BILLING_BACKUP_PREFIX:-walnut_billing}"

DB_PATH="$(sqlite_path_from_dsn "$DB_DSN")"
DB_PATH="$(sqlite_abs_path "$DB_PATH" "$ROOT_DIR")"
if [[ ! -f "$DB_PATH" ]]; then
  echo "SQLite database not found: $DB_PATH" >&2
  exit 1
fi

if [[ "$BACKUP_DIR" != /* ]]; then
  BACKUP_DIR="$ROOT_DIR/$BACKUP_DIR"
fi
mkdir -p "$BACKUP_DIR"

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_FILE="$BACKUP_DIR/${BACKUP_PREFIX}_${TIMESTAMP}.sqlite3"
if [[ -e "$BACKUP_FILE" ]]; then
  BACKUP_FILE="$BACKUP_DIR/${BACKUP_PREFIX}_${TIMESTAMP}_$$.sqlite3"
fi
TMP_FILE="$BACKUP_FILE.partial"

if [[ "$TMP_FILE" == *"'"* ]]; then
  echo "Backup path must not contain a single quote: $TMP_FILE" >&2
  exit 1
fi

rm -f "$TMP_FILE"

sqlite3 "$DB_PATH" 'PRAGMA wal_checkpoint(PASSIVE);' >/dev/null
sqlite3 "$DB_PATH" <<SQL
.timeout 5000
.backup '$TMP_FILE'
SQL

sqlite_verify_integrity "$TMP_FILE"
mv "$TMP_FILE" "$BACKUP_FILE"
sqlite_write_sha256 "$BACKUP_FILE"

META_FILE="$BACKUP_FILE.meta"
{
  printf 'created_at_utc=%s\n' "$TIMESTAMP"
  printf 'source_path=%s\n' "$DB_PATH"
  printf 'backup_file=%s\n' "$BACKUP_FILE"
  printf 'sha256_file=%s.sha256\n' "$BACKUP_FILE"
  printf 'schema_migrations=\n'
  sqlite_print_schema_migrations "$BACKUP_FILE" || true
} > "$META_FILE"

printf 'SQLite backup created\n'
printf '  source: %s\n' "$DB_PATH"
printf '  backup: %s\n' "$BACKUP_FILE"
printf '  sha256: %s.sha256\n' "$BACKUP_FILE"
printf '  meta:   %s\n' "$META_FILE"
