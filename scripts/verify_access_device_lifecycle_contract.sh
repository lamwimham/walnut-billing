#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying access device lifecycle contract...\n'
go test ./internal/service -run 'TestAccess(DeviceAdmin|SessionService_RevokedDevice|SnapshotIssuer_RejectsRevokedDevice)' -count=1
go test ./internal/api/handler -run 'TestAccess(AdminHandler_RevokeDevice|SessionHandler_DeviceRevoked|SnapshotHandler_MapsErrors|LoginChallengeHandler_MapsErrors)' -count=1
go test ./internal/repository/gorm_repo -run 'TestUserDeviceRepo_GetByIDAndPersistRevocation' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
