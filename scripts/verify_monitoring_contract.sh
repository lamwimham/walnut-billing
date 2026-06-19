#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying monitoring and alerting contract...\n'

go test ./internal/service -run 'TestObserved(CheckoutService|PaymentEventService|FulfillmentService|PaymentAdjustmentService|SubscriptionCancellationService|CloudStorageService|AccessSnapshotIssuer)' -count=1
go test ./internal/observability -run 'Test' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1

for metric in \
  subscription_actions_total \
  cloud_sync_total \
  access_snapshots_total \
  admin_actions_total; do
  rg -n "$metric" internal/metrics/metrics.go >/dev/null
done

for decorator in \
  NewObservedSubscriptionCancellationService \
  NewObservedCloudStorageService \
  NewObservedAccessSnapshotIssuer \
  NewObservedAuditService; do
  rg -n "$decorator" internal/app/bootstrap/bootstrap.go internal/observability/audit_observer.go >/dev/null
done

for alert in \
  'checkout failure spike' \
  'webhook failed' \
  'fulfillment failed' \
  'snapshot signing error' \
  'cloud quota overage' \
  'subscription control failed' \
  'admin_actions_total'; do
  rg -in "$alert" docs/RUNBOOK_MONITORING_ALERTS.md >/dev/null
done

for forbidden_label in \
  'metric labels.*`user_id`' \
  'raw `device_id`' \
  'object key' \
  'checkout URL' \
  'webhook secret'; do
  rg -n "$forbidden_label" docs/RUNBOOK_MONITORING_ALERTS.md >/dev/null
done
