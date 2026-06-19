#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying production config contract...\n'
go test ./internal/config -run 'Test(ProductionConfig|LoadReadsCheckoutRiskPolicyEnvConfig)' -count=1
go test ./internal/service -run 'TestCheckoutRedirectPolicy' -count=1
go test ./internal/api/middleware -run 'Test(CORSMiddleware|SecurityHeaders)' -count=1
go test ./internal/api/handler -run 'TestCheckoutHandler_MapsServiceErrors' -count=1
go test ./internal/app/bootstrap -run 'Test(ArchitectureImportBoundaries|BuildRouterAppliesProductionSecurityMiddleware)' -count=1
