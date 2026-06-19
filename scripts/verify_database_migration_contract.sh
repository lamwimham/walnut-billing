#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying database migration contract...\n'
go test ./internal/app/migration -run 'TestRun|TestRunner' -count=1
go test ./internal/config -run 'Test(LoadReadsDatabaseMigrationModeEnvConfig|ProductionConfig)' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
