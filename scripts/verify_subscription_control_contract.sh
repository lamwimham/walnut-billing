#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying subscription control provider contract...\n'
go test ./internal/payment -run 'Test(PaymentService_SubscriptionControl|CreemAdapter_(CancelSubscription|ResumeSubscription))' -count=1
go test ./internal/service -run 'TestSubscriptionCancellationService_' -count=1
go test ./internal/api/handler -run 'TestSubscriptionHandler_' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
