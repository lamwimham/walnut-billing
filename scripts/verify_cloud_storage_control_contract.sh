#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying cloud storage control-plane contract...\n'

go test ./internal/service -run 'TestCloudStorageService' -count=1
go test ./internal/service -run 'TestPlanAwareCloudStorageQuotaPolicy' -count=1
go test ./internal/service -run 'TestAccessSnapshotIssuer_UsesSharedCloudQuotaPolicy|TestAdminCloudStorageService|TestAdminUserAccessSummaryService' -count=1
go test ./internal/api/handler -run 'TestCloudStorageHandler' -count=1
go test ./internal/repository/gorm_repo -run 'TestCloudStorageRepositories' -count=1
go test ./internal/app/migration -run 'TestRunVersionedAppliesBaselineAndRecordsMetadata' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
go test ./internal/config -run 'TestLoadReadsAccessEnvConfig|TestProductionConfigValidationRejectsMissingCriticalSettings' -count=1

for symbol in \
  CloudSyncSession \
  CloudSyncSessionRepository \
  CloudSyncSessionRepo \
  PlanAwareCloudStorageQuotaPolicy \
  CloudStorageQuotaDecision \
  CloudStoragePlanMonthly \
  ACCESS_CLOUD_STORAGE_MONTHLY_QUOTA_MB \
  BuildDownloadTarget \
  GetByObjectKey \
  DeleteObject \
  HeadObject; do
  rg -n "$symbol" internal README.md docs scripts >/dev/null
done

for route in \
  '/users/:user_id/cloud-storage/projects' \
  '/cloud-storage/projects/:project_id/manifests/latest' \
  '/cloud-storage/download-targets'; do
  rg -n "$route" internal/app/bootstrap/router.go >/dev/null
done

for doc in \
  'CloudSyncSession' \
  'plan-aware quota' \
  'download-targets' \
  'ObjectStorageProvider' \
  'provider not configured' \
  'restore metadata'; do
  rg -in "$doc" README.md docs/ADR_CLOUD_STORAGE_PROVIDER.md docs/RUNBOOK_CLOUD_STORAGE_CONTROL_PLANE.md docs/ISSUE_WALNUT_BILLING_CONTROL_PLANE_ROADMAP.md >/dev/null
done

for forbidden in \
  'billing.*receives.*object bytes' \
  'local absolute paths' \
  'Admin cloud read models never expose object keys'; do
  rg -in "$forbidden" docs/ADR_CLOUD_STORAGE_PROVIDER.md docs/RUNBOOK_CLOUD_STORAGE_CONTROL_PLANE.md >/dev/null
done
