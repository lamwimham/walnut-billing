#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying admin test scenario reset contract...\n'
go test ./internal/service -run 'TestAdminTestScenarioResetService' -count=1
go test ./internal/api/handler -run 'TestAdminTestScenarioResetHandler' -count=1
go test ./internal/repository/gorm_repo -run 'TestAdminTestScenarioResetRepo' -count=1
go test ./internal/api/middleware -run 'TestAdminTestWritePermissionIsScopedSeparatelyFromAdminReads|TestAPIKeyAuthPrincipalsSupportsWildcard' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
