#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying access login challenge contract...\n'
go test ./internal/service -run 'TestAccessLoginChallengeService_' -count=1
go test ./internal/api/handler -run 'TestAccess(LoginChallenge|Session)Handler_' -count=1
go test ./internal/repository/gorm_repo -run 'TestAccessLoginChallengeRepo_' -count=1
go test ./internal/config -run 'TestLoadReadsAccessEnvConfig' -count=1
