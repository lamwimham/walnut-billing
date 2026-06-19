#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying webhook operations contract...\n'
go test ./internal/service -run 'TestPaymentEventService_(ProcessorFailureCanBeReprocessed|AdjustmentManualReviewIsAcceptedAndAdminReprocessable|AdjustmentRejectionIsAcceptedWithoutProviderRetry|DuplicateProcessingEventDoesNotReprocess|InvalidSignatureIsRejectedBeforeInbox)' -count=1
go test ./internal/api/handler -run 'TestPaymentEventHandler_(DuplicateAccepted|MapsErrors|ReceiveWebhookMapsTransport)' -count=1
go test ./internal/api/middleware -run 'TestPaymentEventsWritePermissionIsScopedSeparatelyFromRead|TestAPIKeyAuthPrincipalsSupportsWildcard' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
