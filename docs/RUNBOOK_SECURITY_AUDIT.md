# Walnut Billing Security Audit Runbook

This runbook defines production security audit expectations for secrets, provider payloads, PII, and admin actions. It complements `ValidateProduction`: startup blocks unsafe config, while this runbook covers runtime evidence and operator behavior.

## Architecture Boundary

```text
Runtime config / provider payloads / admin actions
  -> handlers normalize transport only
  -> services/projectors expose privacy-safe read models
  -> audit logs store action facts, not secrets
  -> runbooks/scripts verify redaction contracts
```

Secrets stay in config/provider adapters. Raw provider payloads stay in `PaymentEventInbox` storage for verification/reprocess, but admin read models expose only `payload_hash` and status metadata unless a controlled break-glass process is introduced later.

## Secret Redaction Rules

Never log, return, or store in audit details:

- `PAYMENT_CREEM_API_KEY`, `PAYMENT_CREEM_WEBHOOK_SECRET`.
- WeChat private key / API v3 key.
- Alipay private key.
- Admin API keys or `Authorization` header values.
- Checkout URL tokens or provider customer/subscription/checkout IDs in client responses.

Allowed in audit/details:

- Provider name, sandbox mode, operation mode (`import`, `mock`, runtime update).
- Names of sensitive fields changed, e.g. `secret_fields_set=["api_key","webhook_secret"]`.
- Counts and booleans such as `product_map_count`, `has_provider_subscription`, `payload_hash`.

## Runtime Config Update Audit

All runtime provider config updates must write `config.update` audit entries. Audit entries should identify the operator principal name, target provider, sandbox/mode, and field names only.

Example safe audit details:

```json
{"provider":"creem","sandbox":true,"fields_updated":["product_map_present"],"secret_fields_set":["api_key","webhook_secret"],"product_map_count":2}
```

Unsafe details:

```json
{"api_key":"creem_live_xxx","webhook_secret":"whsec_xxx","product_ids":{"sku":"prod_xxx"}}
```

## Admin Read Model Privacy

Admin read APIs are troubleshooting projections, not raw database dumps.

| Surface | Must expose | Must not expose |
|---|---|---|
| `/api/v1/admin/orders` | Walnut order status, amount, event count, latest `payload_hash`, provider presence flags | checkout URL, provider checkout/customer/subscription IDs, idempotency key, raw payload |
| `/api/v1/admin/subscriptions` | masked email, Walnut projection, cancel/resume flags, latest `payload_hash`, provider-control status metadata | raw email, provider event ID, checkout URL, provider customer/subscription ID, idempotency key, raw payload |
| `/api/v1/admin/users/:id/access` | masked email, fingerprint, safe order/event summaries | raw email, raw device id, provider IDs, checkout URL |
| `/api/v1/admin/audit` | projected actor, redacted free text | raw email actors or secrets |
| `/api/v1/admin/payment-events/:id` | event status, attempts, `last_error`, `payload_hash` | no public/support access; ops only, raw payload retained only for controlled reprocess/debug |

## Raw Payload Retention

Current MVP stores raw webhook payload in `PaymentEventInbox.RawPayload` for signature evidence and reprocess debugging. Production policy until an encrypted retention job exists:

- Restrict raw event access to `admin.payment_events.read` ops/admin principals only.
- Do not display raw payload in cross-module read models.
- Do not paste raw payload into tickets; use `payload_hash`, provider event id, and out trade no.
- Review raw payload retention monthly; next hardening step is encryption-at-rest or payload truncation/export-to-secure-vault.

## PII Retention

- Email identity is stored because it anchors access recovery; admin projections must use masked email + fingerprint.
- Audit free text is projected through `AdminPrivacyProjector.RedactFreeText` before display.
- Access challenge audit uses fingerprints/opaque actors for abuse events, not OTP or raw IP.
- Device identifiers must stay service-side; admin responses should use server-owned device row IDs/status.

## Admin Action Review

Before production traffic, verify every write action has scoped permission and audit evidence:

| Action | Permission | Audit expectation |
|---|---|---|
| Runtime payment config update | `admin.payment.write` | `config.update`, target `payment.<provider>`, secret field names only |
| Device revoke | `admin.access_accounts.write` | `access.device.revoke`, operator actor, reason redacted on read |
| Entitlement/credit grant | `admin.entitlements.write` / `admin.credits.write` | grant action with target user, no secret details |
| Risk resolve | `admin.payment_risk.write` | `payment_risk.resolve`, note redacted on read |
| Webhook reprocess | `admin.payment_events.write` | operation visible through event attempts/logs; future work should add explicit audit entry |

## Incident Review Checklist

- [ ] No raw secrets in application logs for the incident window.
- [ ] No raw webhook payload or full headers in tickets/chat.
- [ ] Admin read model response contains only payload hash/provider presence flags.
- [ ] Any manual provider config update has a `config.update` audit entry.
- [ ] Any customer compensation uses service/admin APIs, not direct SQL.
- [ ] If raw payload was inspected, record operator, reason, event id, payload hash, and retention follow-up.

## Quality Gate

```bash
scripts/verify_security_audit_contract.sh
go test ./...
git diff --check
```
