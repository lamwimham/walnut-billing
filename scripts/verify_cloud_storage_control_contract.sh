#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying cloud storage control-plane contract...\n'

go test ./internal/service -run 'TestCloudStorageService' -count=1
go test ./internal/api/handler -run 'TestCloudStorageHandler' -count=1
go test ./internal/repository/gorm_repo -run 'TestCloudStorageRepositories' -count=1
go test ./internal/app/migration -run 'TestRunVersionedAppliesBaselineAndRecordsMetadata' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1

for symbol in \
  CloudSyncSession \
  CloudSyncSessionRepository \
  CloudSyncSessionRepo \
  BuildDownloadTarget \
  DeleteObject \
  HeadObject; do
  rg -n "$symbol" internal >/dev/null
done

for route in \
  '/users/:user_id/cloud-storage/projects' \
  '/cloud-storage/projects/:project_id/manifests/latest'; do
  rg -n "$route" internal/app/bootstrap/router.go >/dev/null
done

for doc in \
  'CloudSyncSession' \
  'ObjectStorageProvider' \
  'provider not configured' \
  'restore metadata'; do
  rg -in "$doc" docs/ADR_CLOUD_STORAGE_PROVIDER.md docs/RUNBOOK_CLOUD_STORAGE_CONTROL_PLANE.md >/dev/null
done

for forbidden in \
  'billing.*receives.*object bytes' \
  'local absolute paths' \
  'Admin cloud read models never expose object keys'; do
  rg -in "$forbidden" docs/ADR_CLOUD_STORAGE_PROVIDER.md docs/RUNBOOK_CLOUD_STORAGE_CONTROL_PLANE.md >/dev/null
done
