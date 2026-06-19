# Monitoring And Alerts Runbook

This runbook closes the WCP-6 monitoring slice for small-scale paid production. It defines the observability contract, alert rules, owners, and first-response actions for checkout, webhook, fulfillment, access snapshot signing, cloud quota, subscription control, and audited admin actions.

## Architecture

```text
application services
  -> observer/decorator interfaces
  -> internal/observability adapters
  -> internal/metrics Prometheus series + structured logs
  -> dashboard / alertmanager / runbooks
```

Handlers only map HTTP transport. Provider adapters only translate provider contracts. Metrics and logs are emitted from service decorators so Creem test/prod switching remains configuration-only and cannot leak into Walnut access gates.

## Metrics Contract

| Metric | Labels | Alert Focus |
|---|---|---|
| `commerce_checkouts_total` | `provider`, `sku_code`, `status`, `error_kind` | checkout failure spike, provider timeout, risk/policy blocks |
| `payment_events_total` | `operation`, `provider`, `event_type`, `status`, `error_kind` | webhook failed, signature failure, amount/currency mismatch |
| `fulfillments_total` | `sku_code`, `order_type`, `status`, `error_kind` | paid but not fulfilled, rule/repository failures |
| `payment_adjustments_total` | `event_type`, `status`, `policy_action`, `error_kind` | refund/dispute manual-review queue and policy rejects |
| `subscription_actions_total` | `operation`, `sku_code`, `status`, `error_kind` | cancel/resume provider-control failures |
| `cloud_sync_total` | `operation`, `provider`, `status`, `error_kind` | quota overage, provider not configured, sync failures |
| `access_snapshots_total` | `status`, `error_kind` | snapshot signing/config/revoked-device errors |
| `admin_actions_total` | `action`, `success` | audited admin writes and failed operator actions |
| `http_requests_total` | `method`, `path`, `status` | route-level 5xx/4xx spikes |

Low-cardinality rule: metric labels must not include `user_id`, `out_trade_no`, raw `device_id`, `provider_event_id`, checkout URL, object key, product ID, API key, webhook secret, raw payload, or file content. Those identifiers belong only in privacy-safe logs/admin views when needed for incident triage.

## Critical Alerts

Example PromQL uses a 5 minute window for pages and a 15 minute window for tickets. Tune thresholds after the first week of production traffic.

| Alert | Severity | PromQL Sketch | Owner | First Action |
|---|---|---|---|---|
| Checkout failure spike | page | `sum(increase(commerce_checkouts_total{status="failed"}[5m])) >= 3` | engineering | Check provider status, `error_kind`, recent deploy, and redirect allowlist/config. |
| Checkout provider timeout | page | `sum(increase(commerce_checkouts_total{error_kind="provider_timeout"}[5m])) >= 1` | engineering | Verify Creem/test-api reachability and client retry guidance. |
| Webhook failed | page | `sum(increase(payment_events_total{status="failed"}[5m])) >= 1` | ops + engineering | Open `/api/v1/admin/payment-events?status=failed`; follow webhook operations runbook. |
| Webhook signature failure | page | `sum(increase(payment_events_total{error_kind="signature_verification_failed"}[5m])) >= 1` | engineering | Check webhook secret, TLS/proxy raw body behavior, and provider endpoint. |
| Amount/currency mismatch | page | `sum(increase(payment_events_total{error_kind=~"amount_mismatch|currency_mismatch"}[5m])) >= 1` | finance + engineering | Do not force paid. Compare SKU/product map and provider payload. |
| Fulfillment failed | page | `sum(increase(fulfillments_total{status="failed"}[5m])) >= 1` | engineering | Fix rule/repository dependency, then admin reprocess the event. |
| Snapshot signing error | page | `sum(increase(access_snapshots_total{error_kind="signature_error"}[5m])) >= 1` | engineering | Roll back signer/config changes; verify prod Ed25519 key and key id. |
| Cloud quota overage spike | ticket | `sum(increase(cloud_sync_total{error_kind="over_quota"}[15m])) >= 5` | support | Check user quota/admin cloud metadata; explain upgrade/cleanup path. |
| Cloud provider not configured | page | `sum(increase(cloud_sync_total{error_kind="provider_not_configured"}[5m])) >= 1` | engineering | Fix object storage provider config; software access remains unaffected. |
| Subscription control failed | page | `sum(increase(subscription_actions_total{status="failed"}[5m])) >= 1` | engineering | Check Creem subscription control result; local cancellation/resume fact should not be written on provider failure. |
| Admin write failed | ticket | `sum(increase(admin_actions_total{success="false"}[15m])) >= 1` | ops lead | Review audit details and operator permissions; repeat only after cause is understood. |

## Structured Logs

| Event | Fields | Notes |
|---|---|---|
| `commerce_checkout_observed` | provider, sku_code, user_id, out_trade_no, status, error_kind, policy fields | Checkout URL and provider customer/checkout ids are never logged. |
| `payment_event_observed` | operation, provider, provider_event_id, event_type, out_trade_no, inbox_status, attempts, error_kind | Raw payload and full headers are never logged. |
| `commerce_fulfillment_observed` | out_trade_no, user_id, sku_code, order_type, status, execution_count, error_kind | Use with fulfillment executions/admin order read model. |
| `payment_adjustment_observed` | provider, provider_event_id, event_type, policy_action, risk flag metadata | Use for refund/dispute/cancel triage. |
| `subscription_action_observed` | operation, user_id, sku_code, status, error_kind, cancellation projection | Provider subscription id stays in payment/order metadata, not logs. |
| `cloud_sync_observed` | operation, provider, user_id, project ids, requested/used/quota bytes, error_kind | Do not log object keys, upload URLs, manifest hash, local path, or content. |
| `access_snapshot_observed` | user_id, device_present, device_status, signature key metadata, license_state, error_kind | Does not log raw device id or snapshot signature. |

## Triage Flows

### Payment Succeeded But Not Unlocked

1. Check `payment_events_total{status="failed"}` and `fulfillments_total{status="failed"}` for the incident window.
2. In admin, locate the order by `out_trade_no` or masked user summary.
3. If payment event is `failed`, follow `docs/RUNBOOK_WEBHOOK_OPERATIONS.md` and reprocess after fixing the root cause.
4. If fulfillment failed, fix catalog/rule/repository dependency and reprocess the payment event; do not manually insert grants unless a separate audited emergency change is approved.
5. Ask the user to refresh signed access snapshot after fulfillment succeeds.

### Snapshot Signing Error

1. Confirm `access_snapshots_total{error_kind="signature_error"}` and route 5xx around `/users/:user_id/access/snapshot`.
2. Check recent signer/key deployment, `ACCESS_SNAPSHOT_SIGNER`, `ACCESS_SNAPSHOT_PRIVATE_KEY`, and `ACCESS_SNAPSHOT_KEY_ID`.
3. Roll back to the last known-good signer if paid users cannot refresh access.
4. After recovery, verify snapshots have the expected `signature_key_id` and `signature_algorithm`.

### Cloud Quota Overage

1. Confirm `cloud_sync_total{error_kind="over_quota"}` by operation.
2. Use `/api/v1/admin/cloud-storage/usage` or `/api/v1/admin/users/:id/cloud-storage/projects` to inspect metadata only.
3. If usage is expected, support explains cleanup/upgrade. If usage looks incorrect, engineering checks active/replaced object metadata and manifest commits.
4. Do not inspect object bytes or raw local paths.

### Subscription Cancel/Resume Failure

1. Confirm `subscription_actions_total{status="failed"}` and `error_kind`.
2. If `control_failed`, check provider availability and retry with the same idempotency key after the provider recovers.
3. If `control_unavailable`, verify provider subscription-control adapter registration.
4. Confirm no local `SubscriptionCancellation` fact was written when provider control failed.

## Dashboard Minimum

A production dashboard must show:

- Checkout attempts by status/error kind and provider.
- Webhook receive/reprocess statuses and signature/amount/currency errors.
- Fulfillment failures by SKU/order type.
- Snapshot signing failures and access snapshot 5xx.
- Cloud sync blocked/failed operations, including quota overage.
- Subscription cancel/resume status.
- Recent audited admin writes by action/success.

## Verification

Before declaring WCP-6 closed, run:

```bash
scripts/verify_monitoring_contract.sh
scripts/verify_webhook_operations_contract.sh
scripts/verify_security_audit_contract.sh
go test ./...
git diff --check
```
