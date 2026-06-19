#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying admin subscription read contract...\n'
go test ./internal/service -run 'TestAdminSubscriptionService' -count=1
go test ./internal/api/handler -run 'TestAdminSubscriptionHandler' -count=1
go test ./internal/repository/gorm_repo -run 'TestAdminSubscriptionReadRepo' -count=1
go test ./internal/api/middleware -run 'TestSubscriptionsReadPermissionIsScopedSeparatelyFromOrdersAndPaymentEvents|TestAPIKeyAuthPrincipalsSupportsWildcard' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
