#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/sqlite.sh
source "$ROOT_DIR/scripts/lib/sqlite.sh"

usage() {
  cat <<USAGE
Usage: scripts/verify_sqlite_restore.sh <backup_file> [restore_drill_dir]

Copies a backup into a disposable restore-drill directory, verifies checksum
when a .sha256 file exists, runs SQLite integrity checks, and confirms core
Walnut Billing production tables are present.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ -z "${1:-}" ]]; then
  usage
  exit 1
fi

require_sqlite3
cd "$ROOT_DIR"

BACKUP_FILE="$1"
RESTORE_DIR="${2:-${WALNUT_BILLING_RESTORE_DRILL_DIR:-$ROOT_DIR/.tmp/restore-drill/$(basename "$BACKUP_FILE" .sqlite3)}}"
if [[ "$BACKUP_FILE" != /* ]]; then
  BACKUP_FILE="$ROOT_DIR/$BACKUP_FILE"
fi
if [[ "$RESTORE_DIR" != /* ]]; then
  RESTORE_DIR="$ROOT_DIR/$RESTORE_DIR"
fi
if [[ ! -f "$BACKUP_FILE" ]]; then
  echo "Backup file not found: $BACKUP_FILE" >&2
  exit 1
fi

SHA_FILE="$BACKUP_FILE.sha256"
if [[ -f "$SHA_FILE" ]]; then
  sqlite_verify_sha256 "$SHA_FILE"
fi

mkdir -p "$RESTORE_DIR"
RESTORED_DB="$RESTORE_DIR/restored.sqlite3"
cp "$BACKUP_FILE" "$RESTORED_DB"

sqlite_verify_integrity "$RESTORED_DB"
required_tables=(
  schema_migrations
  users
  orders
  payment_event_inboxes
  fulfillment_executions
  entitlement_grants
  subscription_cancellations
)
sqlite_require_tables "$RESTORED_DB" "${required_tables[@]}"

printf 'SQLite restore drill passed\n'
printf '  backup:   %s\n' "$BACKUP_FILE"
printf '  restored: %s\n' "$RESTORED_DB"
printf '  table counts:\n'
sqlite_print_table_counts "$RESTORED_DB" "${required_tables[@]}"
