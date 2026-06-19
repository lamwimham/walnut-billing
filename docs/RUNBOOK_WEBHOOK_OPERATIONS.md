# Walnut Billing Webhook Operations Runbook

This runbook defines the production workflow for webhook retry, dead-letter handling, and admin reprocess. It does not add provider-specific branches to handlers: all provider payloads still enter the same `PaymentEventService -> PaymentEventProcessor` inbox boundary.

## Architecture Boundary

```text
Provider webhook
  -> PaymentEventHandler        # transport only: raw body, headers, params
  -> PaymentEventService        # signature verification, inbox idempotency, retry state
  -> PaymentEventProcessor      # order, fulfillment, adjustment, renewal policies
  -> PaymentEventInbox          # operator-visible queue and dead-letter source
  -> Admin reprocess endpoint   # POST /api/v1/admin/payment-events/:id/reprocess
```

Handlers do not decide business policy. Provider adapters only verify/normalize payloads. Retry/dead-letter decisions are based on Walnut-owned inbox status, attempts, `last_error`, and the related order/fulfillment/risk facts.

## Inbox Status Semantics

| status | Meaning | Provider response | Operator action |
|---|---|---|---|
| `received` | Verified and stored but no processor currently completed it | 202/accepted if processor unavailable | Reprocess after processor/dependency is healthy |
| `processing` | Currently being processed; duplicate provider delivery should not run another processor | 202/accepted | Wait; investigate if stuck longer than expected |
| `processed` | Terminal success; order/fulfillment/adjustment effects converged | 200 | No action |
| `ignored` | Terminal non-actionable event type | 202/accepted | No action unless provider mapping should be expanded |
| `failed` | Processor failed for a recoverable or unknown reason | 500 on first attempt, visible in inbox | Fix root cause, then reprocess |
| `review_required` | Policy requires human review, usually refund/dispute/low-usage review | 202/accepted | Review business decision, then reprocess only after policy/input is corrected |
| `policy_rejected` | Policy terminally rejected the requested adjustment | 202/accepted | Treat as dead-letter unless business policy changes |

Operational dead-letter queue today is `status in (failed, review_required, policy_rejected)` after the owning team decides no automatic retry should continue. Do not add a second dead-letter table until there is an actual worker/retention need; the inbox is already the durable audit source.

## Triage Queries

Set common shell variables:

```bash
BASE_URL="${BASE_URL:-http://localhost:8082}"
AUTH_HEADER="Authorization: Bearer ${ADMIN_KEY:-ops-key}"
```

List active operator queues:

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events?status=failed&limit=50" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/payment-events?status=review_required&limit=50" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/payment-events?status=policy_rejected&limit=50" \
  -H "$AUTH_HEADER"
```

Inspect a single event:

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events/<payment_event_id>" \
  -H "$AUTH_HEADER"
```

Correlate using only Walnut-safe fields:

- `out_trade_no`: inspect `/api/v1/admin/orders?out_trade_no=...`.
- `provider`, `provider_event_id`, `event_type`: compare with provider dashboard.
- `payload_hash`: confirms payload identity without exposing raw payload.
- `attempts`, `last_error`: decide whether retry is safe.

## Retry Decision Matrix

Retry only after the root cause is fixed.

| symptom / `last_error` | Likely cause | Safe next step |
|---|---|---|
| temporary DB / transaction error | transient storage failure | Reprocess after DB health is green |
| fulfillment rule missing | bad `FULFILLMENT_RULES_JSON` or catalog mismatch | Fix config/rules, restart if needed, then reprocess |
| unknown order / missing `out_trade_no` | provider metadata/product mapping issue | Fix provider metadata or create compensating Walnut order only through a controlled admin path; do not blindly reprocess |
| amount mismatch / currency mismatch | SKU/product/provider mapping mismatch or fraud | Treat as high-risk dead-letter; do not force paid until finance confirms |
| `review_required` | policy intentionally paused adjustment | Resolve review, update policy/threshold if needed, then reprocess once |
| `policy_rejected` | terminal policy rejection | Do not reprocess unless policy was intentionally changed and approval is recorded |
| signature verification failed | provider secret/proxy/raw-body issue | Event is rejected before inbox; fix webhook secret/proxy and replay from provider dashboard if available |

## Reprocess Procedure

1. Confirm the event is not already `processed` or `ignored`:

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events/<payment_event_id>" \
  -H "$AUTH_HEADER"
```

2. Check related order and fulfillment state:

```bash
curl -sS "$BASE_URL/api/v1/admin/orders?out_trade_no=<out_trade_no>" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/fulfillments?out_trade_no=<out_trade_no>" \
  -H "$AUTH_HEADER"
```

3. Reprocess once the root cause is fixed:

```bash
curl -sS -X POST "$BASE_URL/api/v1/admin/payment-events/<payment_event_id>/reprocess" \
  -H "$AUTH_HEADER"
```

4. Verify convergence:

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events/<payment_event_id>" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/orders?out_trade_no=<out_trade_no>" \
  -H "$AUTH_HEADER"
```

Expected success: event `status=processed`, `attempts` increments, related order/fulfillment/access state is consistent. If it remains `failed`, stop after one manual retry and escalate with `last_error`, `payload_hash`, `out_trade_no`, and deployment version.

## Dead-Letter Procedure

Use dead-letter handling when reprocess is unsafe or repeatedly fails.

1. Preserve evidence: event id, provider, provider event id, out trade no, payload hash, attempts, last error, and related order summary.
2. Classify owner:

| owner | Cases |
|---|---|
| Engineering | processor bug, migration/schema issue, unexpected panic/error kind |
| Finance/Ops | amount/currency mismatch, suspected duplicate charge, refund approval |
| Provider/Ops | signature failures, provider dashboard replay gaps, wrong webhook endpoint |
| Product/Ops | policy changes for refund/dispute/low-usage rules |

3. Do not mutate DB rows manually. Leave the inbox row in `failed`, `review_required`, or `policy_rejected` so admin views and metrics keep surfacing it.
4. If a customer needs manual compensation, use dedicated admin service paths such as grant/revoke/risk resolution, not direct SQL.
5. Close the incident only after the final customer-visible projection is confirmed through admin read models and access snapshot refresh.

## Alerts

Minimum alerts before production traffic:

- `payment_events_total{status="failed"}` increases for 5 minutes.
- `payment_events_total{error_kind="signature_verification_failed"}` increases at all.
- `payment_events_total{error_kind="amount_mismatch"}` or `currency_mismatch` is non-zero.
- `payment_adjustments_total{status="review_required"}` backlog exceeds the ops SLA.
- No successful `payment_events_total{status="processed"}` for a period with expected checkout activity.

## Security And Privacy

- Never paste raw webhook payload, API key, webhook secret, checkout URL token, or full headers into incident tickets.
- Use `payload_hash` and provider event id for correlation.
- `admin.payment_events.read` may inspect events; `admin.payment_events.write` is required for reprocess.
- Support read-only principals must not hold `admin.payment_events.write`.

## Quality Gate

Run before closing webhook operations work:

```bash
scripts/verify_webhook_operations_contract.sh
go test ./...
git diff --check
```
