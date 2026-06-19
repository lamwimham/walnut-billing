# Walnut Billing Backup / Restore Runbook

This runbook covers the current production baseline: SQLite with WAL mode and a versioned migration ledger. The goal is an operator-repeatable backup and restore drill before small-scale paid traffic. PostgreSQL migration remains a later WCP-6 expansion path.

## Architecture Boundary

Backup and restore are operational concerns, not business services:

```text
DATABASE_DSN
  -> scripts/backup_sqlite.sh
       -> SQLite online .backup snapshot
       -> integrity_check / foreign_key_check
       -> .sha256 + .meta
  -> scripts/verify_sqlite_restore.sh
       -> disposable restore copy
       -> checksum + core table checks
       -> schema_migrations visibility
```

Application code owns schema evolution in `internal/app/migration`; backup scripts only copy and verify database state.

## Prerequisites

- `sqlite3` is installed on the host or maintenance container.
- `sha256sum` or `shasum` is installed.
- `DATABASE_DSN` points to a file-backed SQLite DB, not `:memory:` or `mode=memory`.
- Production runs with `DATABASE_MIGRATION_MODE=versioned` so `schema_migrations` exists.
- Backup storage is outside the application container writable layer and is copied off-host.

For Docker Compose, the default volume maps `./data` into `/app/data`; use a host path such as `./backups` or a mounted durable backup volume for backup artifacts.

## Create A Backup

Use the configured `DATABASE_DSN`:

```bash
DATABASE_DSN=./data/walnut_billing.db \
WALNUT_BILLING_BACKUP_DIR=./backups \
scripts/backup_sqlite.sh
```

Or pass explicit source and destination:

```bash
scripts/backup_sqlite.sh ./data/walnut_billing.db ./backups
```

The script uses SQLite's online `.backup` API, so it can run while the service is live. It writes:

- `walnut_billing_<UTC>.sqlite3`: consistent backup snapshot.
- `walnut_billing_<UTC>.sqlite3.sha256`: checksum for transfer/restore verification.
- `walnut_billing_<UTC>.sqlite3.meta`: source path and `schema_migrations` summary.

After each backup, copy all three files to off-host storage. Do not rely on the same VM/disk that stores the primary SQLite DB.

## Verify Restore

Run a disposable restore drill for every backup before marking it usable:

```bash
scripts/verify_sqlite_restore.sh ./backups/walnut_billing_20260619T000000Z.sqlite3 ./.tmp/restore-drill/latest
```

The restore verifier checks:

- `.sha256` checksum when present.
- `PRAGMA integrity_check`.
- `PRAGMA foreign_key_check`.
- Core production tables: `schema_migrations`, `users`, `orders`, `payment_event_inboxes`, `fulfillment_executions`, `entitlement_grants`, `subscription_cancellations`.

Expected success output includes table counts and the disposable restored DB path.

## Restore Production

Use this only during incident recovery or a planned rollback window.

1. Identify the target backup and verify it first:

```bash
scripts/verify_sqlite_restore.sh ./backups/walnut_billing_<UTC>.sqlite3 ./.tmp/restore-drill/pre-prod
```

2. Stop the billing service to avoid concurrent writes:

```bash
docker compose stop walnut-billing
```

3. Preserve the current damaged DB before replacing it:

```bash
mkdir -p ./backups/pre-restore
cp ./data/walnut_billing.db ./backups/pre-restore/walnut_billing_before_restore.sqlite3
cp ./data/walnut_billing.db-wal ./backups/pre-restore/ 2>/dev/null || true
cp ./data/walnut_billing.db-shm ./backups/pre-restore/ 2>/dev/null || true
```

4. Restore the verified snapshot:

```bash
cp ./backups/walnut_billing_<UTC>.sqlite3 ./data/walnut_billing.db
rm -f ./data/walnut_billing.db-wal ./data/walnut_billing.db-shm
```

5. Ensure production migration mode remains versioned:

```bash
grep '^DATABASE_MIGRATION_MODE=versioned' .env
```

6. Start the service and check health:

```bash
docker compose up -d walnut-billing
curl -fsS http://localhost:8082/health
```

7. Confirm application-level recovery paths:

```bash
curl -fsS http://localhost:8082/api/v1/admin/payment-events \
  -H "Authorization: Bearer $ADMIN_KEY" >/dev/null
curl -fsS http://localhost:8082/api/v1/admin/orders \
  -H "Authorization: Bearer $ADMIN_KEY" >/dev/null
```

8. Record the restore in the incident log: backup filename, checksum, operator, reason, service stop/start time, and any lost writes window.

## Backup Schedule And Retention

Minimum pre-launch policy:

- Before every deployment or migration: one verified backup and restore drill.
- Hourly while paid checkout is enabled.
- Daily retained for 30 days.
- Weekly retained for 12 weeks.
- Copy each backup off-host within 15 minutes.

For higher payment volume, shorten the interval or move to PostgreSQL with point-in-time recovery.

## PostgreSQL Migration Path

SQLite remains acceptable for MVP if backups are verified and volume is low. Move to PostgreSQL when any of these happen:

- Concurrent write load causes sustained SQLite busy timeouts.
- RPO/RTO requirements require point-in-time recovery.
- Operational policy requires managed backups, replicas, or cross-region restore.
- Admin analytics start competing with checkout/webhook writes.

The future Postgres path should keep the same boundaries: migration runner owns schema versions, services remain repository-port based, and backup/restore automation stays outside handlers/services.

## Quality Gate

Run before closing WCP-6 backup work or before production deployment:

```bash
scripts/verify_sqlite_backup_contract.sh
scripts/verify_database_migration_contract.sh
scripts/verify_production_config_contract.sh
go test ./...
git diff --check
```
