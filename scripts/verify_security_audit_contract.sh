#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
mkdir -p "$GOCACHE"

printf 'Verifying security audit contract...\n'
go test ./internal/api/handler -run 'Test(CheckoutHandler_(CreateCheckoutSession|ProviderFailureDoesNotExposeProviderErrorBody)|ConfigHandler_UpdateCreemConfigAuditsWithoutSecrets|AdminHandler_GetAuditLogs_RedactsEmailActors)' -count=1
go test ./internal/service -run 'Test(AdminOrderServiceProjectsSafeOrderDiagnostics|AdminSubscriptionServiceProjectsPrivacySafeSubscriptions|ObservedPaymentEventService_ClassifiesFailedReceiveWithoutRawPayload)' -count=1
go test ./internal/api/middleware -run 'TestPaymentEventsWritePermissionIsScopedSeparatelyFromRead|TestAPIKeyAuthPrincipalsAttachesPrincipalAndRequiresPermission' -count=1
go test ./internal/app/bootstrap -run 'TestArchitectureImportBoundaries' -count=1
