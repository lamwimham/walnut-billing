#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying Creem sandbox adapter contract...\n'
go test ./internal/payment -run 'TestCreemAdapter_(ValidatesRequiredProductMappings|RejectsEnvironmentMixing|AcceptsTestModeDefaults|CancelSubscription|ResumeSubscription|Verify|RejectsBadWebhookSignature)|TestCreemWebhookFixturesNormalizeToWalnutEvents|TestCreemProductMap' -count=1
