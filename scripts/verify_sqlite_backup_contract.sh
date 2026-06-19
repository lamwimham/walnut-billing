#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TMP_ROOT="${TMPDIR:-/tmp}/walnut-billing-backup-contract.$$"
trap 'rm -rf "$TMP_ROOT"' EXIT
mkdir -p "$TMP_ROOT/backups" "$TMP_ROOT/restore"
DB_PATH="$TMP_ROOT/source.sqlite3"
BACKUP_OUTPUT="$TMP_ROOT/backup.out"

sqlite3 "$DB_PATH" <<'SQL'
CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL);
INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES('202606190001', 'baseline_control_plane_schema', 'fixture-checksum', '2026-06-19T00:00:00Z');
CREATE TABLE users(id TEXT PRIMARY KEY);
CREATE TABLE orders(id TEXT PRIMARY KEY);
CREATE TABLE payment_event_inboxes(id TEXT PRIMARY KEY);
CREATE TABLE fulfillment_executions(id TEXT PRIMARY KEY);
CREATE TABLE entitlement_grants(id TEXT PRIMARY KEY);
CREATE TABLE subscription_cancellations(id TEXT PRIMARY KEY);
CREATE TABLE cloud_projects(id TEXT PRIMARY KEY);
CREATE TABLE cloud_sync_sessions(id TEXT PRIMARY KEY);
CREATE TABLE cloud_manifests(id TEXT PRIMARY KEY);
CREATE TABLE cloud_objects(id TEXT PRIMARY KEY);
INSERT INTO users(id) VALUES('usr_fixture');
SQL

scripts/backup_sqlite.sh "$DB_PATH" "$TMP_ROOT/backups" >"$BACKUP_OUTPUT"
BACKUP_FILE="$(find "$TMP_ROOT/backups" -name 'walnut_billing_*.sqlite3' -type f | head -n 1)"
if [[ -z "$BACKUP_FILE" ]]; then
  echo "backup contract did not create a backup file" >&2
  cat "$BACKUP_OUTPUT" >&2 || true
  exit 1
fi
scripts/verify_sqlite_restore.sh "$BACKUP_FILE" "$TMP_ROOT/restore"
