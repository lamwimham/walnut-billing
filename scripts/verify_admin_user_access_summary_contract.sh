#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying admin user access summary contract...\n'
go test ./internal/service -run 'TestAdminUserAccessSummaryService' -count=1
go test ./internal/api/handler -run 'TestAccessAdminHandler_(GetUserAccessSummary|ListAccounts|RevokeDevice)' -count=1
go test ./internal/repository/gorm_repo -run 'Test(AdminUserAccessSummaryReadRepo|AccessAccountReadRepo)' -count=1
go test ./internal/api/middleware -run 'TestUsersReadPermissionIsScopedSeparatelyFromAccessAccounts|TestAPIKeyAuthPrincipalsSupportsWildcard' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
