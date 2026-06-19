# walnut Billing Server

License and billing microservice for walnut products. Single-binary Go service with SQLite backend.

## Architecture

`walnut-billing` is evolving into Walnut's commercial control plane. Startup assembly lives in `internal/app/bootstrap`; HTTP routes are registered by logical module owners so new capabilities do not keep expanding `cmd/server/main.go`. See `docs/architecture/MODULE_BOUNDARIES.md` for dependency rules.

```
walnut-billing/
├── cmd/server/main.go              # Process entry point + graceful shutdown
├── internal/
│   ├── app/bootstrap/              # Config, DB, DI, provider setup, module route registrars
│   ├── config/                     # Viper-based configuration
│   ├── domain/models.go            # Entities (Product, License, Order, User, Grant)
│   ├── generator/                  # Factory pattern: license key generation
│   ├── payment/                    # Adapter pattern: Creem + legacy WeChat/Alipay
│   ├── repository/                 # Repository pattern: interfaces + GORM impl
│   ├── service/                    # Business logic with UnitOfWork transactions
│   └── api/
│       ├── handler/                # HTTP handlers (separated by concern)
│       └── middleware/             # Recovery, Logger, RateLimit, Auth, RequestID
├── .env.example                    # Configuration template
├── Dockerfile                      # Multi-stage production build
└── docker-compose.yml              # Local/production deployment
```

## Quick Start

### Local Development
```bash
# 1. Copy and configure
cp .env.example .env

# 2. Build and run
go build -o walnut-billing ./cmd/server
./walnut-billing

# 3. Health check
curl http://localhost:8082/ping
```

### Docker Deployment
```bash
# 1. Configure
cp .env.example .env
# Edit .env with your payment credentials

# 2. Deploy
docker compose up -d

# 3. Check health
curl http://localhost:8082/health
```

## API Reference

### Client Endpoints (walnut Desktop App)

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/verify` | `{key, device_id}` | License info or error |
| POST | `/api/v1/activate` | `{key, device_id}` | Activation confirmation |
| POST | `/api/v1/deactivate` | `{key, device_id}` | Deactivation confirmation |

### Order & Payment

Current commercialization work focuses on overseas hosted checkout providers. Creem is the first production target; WeChat/Alipay remain legacy-compatible for existing license flows and are not the focus of new development.

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/orders` | `{product_code}` | `{order: {out_trade_no, license_key, amount}}` |
| POST | `/api/v1/orders/pay` | `{out_trade_no, provider}` | `{payment_url}` |
| GET | `/api/v1/orders/:out_trade_no` | — | Order status + payment info |

### Commerce Checkout

The commerce checkout facade is the provider-agnostic entry point for future SKU-based purchases. It creates a Walnut-owned order first, then asks the selected payment adapter for a checkout session. Provider-specific product IDs and checkout details stay inside `walnut-billing`.

Checkout policies use a service-owned software subscription projection before any provider call. Rejections return stable machine-readable reasons: `already_lifetime`, `subscription_active`, `cancel_at_period_end`, or `payment_risk_hold`; clients should use these reasons to show keep-access/manage-subscription/resume/manual-review states instead of guessing from SKU or provider metadata.

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/commerce/checkout-sessions` | `{user_id, sku_code, provider, success_url, cancel_url, idempotency_key}` | `{order, checkout_url, provider}` |

Development builds register a `mock` checkout provider. When `PAYMENT_MOCK_CHECKOUT_BASE_URL` is empty, checkout URLs point to the local billing server (`http://localhost:${SERVER_PORT}/checkout/...`) and render a hosted-checkout stand-in with a "Simulate payment success" action. Creem can be enabled as a hosted checkout adapter through `PAYMENT_CREEM_*`; PC/mobile clients still call the same Walnut checkout facade and never depend on Creem IDs.

### Commerce Fulfillment

Paid commerce orders are fulfilled through Walnut-owned rules, not through provider state. `FulfillmentService` reads the paid order SKU, executes configured rules, writes `FulfillmentExecution` rows for idempotency/audit, and grants only stable Walnut targets such as `EntitlementGrant` and `CreditTransaction`.

`pro_own_ai_monthly` and `pro_own_ai_lifetime` currently grant only the implemented Pro access entitlements: `editorial.studio` and `cloud.storage`. Draft or future entitlement IDs stay out of default fulfillment until the corresponding feature gates are implemented. AI usage stays bring-your-own-key. Legacy credit SKUs remain hidden compatibility records until hosted AI plans are introduced.

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/v1/admin/fulfillments?out_trade_no=&user_id=&sku_code=&status=` | List fulfillment executions for audit/reprocess diagnostics |
| POST | `/api/v1/admin/credits/buckets/expire` | Expire due credit buckets in Walnut-owned storage |

Fulfillment rules can be supplied through `FULFILLMENT_RULES_JSON`; the dev defaults include the Own AI SKUs plus hidden legacy SKU compatibility. The production path uses UnitOfWork so order status, entitlement grants, credit ledger rows, buckets, and fulfillment executions converge safely under retries.

### Subscription Renewal

Subscription webhooks are normalized into Walnut-owned events before they affect access: `payment.renewal_paid`, `payment.renewal_failed`, and `payment.subscription_expired`. `SubscriptionRenewalService` applies a configurable policy: paid renewals reuse `FulfillmentService`, failed renewals create a short `subscription_grace` entitlement grant without credits, and expiry events can expire grace grants or let them naturally expire.

Creem-specific statuses remain inside the payment adapter. PC/mobile gates still consume only Walnut projections: `EntitlementSnapshot`, credit balances, and signed access snapshots. The software subscription projection normalizes active monthly, `cancel_at_period_end`, and lifetime states from Walnut grants plus cancellation facts; signed snapshots expose the same `subscription_status`, `current_period_ends_at`, and `cancel_at_period_end` values.

Cancel/resume uses an optional `SubscriptionControlProvider` port. Mock and Creem implement it; legacy providers return `subscription_control_unavailable`. The service calls the provider first using the order's provider subscription id, then writes Walnut `SubscriptionCancellation` facts and order metadata. If the provider call fails, no local cancellation/resume fact is written and clients can retry with the same idempotency key. Creem test mode uses the same subscription endpoints as production (`POST /v1/subscriptions/{id}/cancel` and `POST /v1/subscriptions/{id}/resume`); only the configured base URL/key/product map change.

| Method | Path | Notes |
|--------|------|-------|
| POST | `/api/v1/commerce/subscriptions/cancel` | Mark monthly renewal as `cancel_at_period_end`; current-period Pro access remains active |
| POST | `/api/v1/commerce/subscriptions/resume` | Clear the cancel-at-period-end fact and return the active subscription projection |

Stable subscription-control errors:

| code | HTTP | Meaning |
|------|-----:|---------|
| `subscription_control_unavailable` | 409 | The selected provider does not support hosted subscription cancel/resume |
| `subscription_control_failed` | 502 | The provider subscription API failed; local Walnut facts were not changed |
| `subscription_not_found` | 404 | No active/cancel-at-period-end monthly subscription exists for the user/SKU |

### Payment Webhook Inbox

New commerce providers should send payment events to the provider-agnostic webhook inbox. The inbox verifies provider payloads through the payment adapter, deduplicates by `provider + provider_event_id`, records processing attempts, and supports admin reprocessing.

| Method | Path | Notes |
|--------|------|-------|
| POST | `/api/v1/webhooks/:provider` | Provider-agnostic webhook inbox for commerce events |
| GET | `/api/v1/admin/payment-events?provider=&status=&event_type=&out_trade_no=` | List inbox events |
| GET | `/api/v1/admin/payment-events/:id` | Inspect one inbox event |
| POST | `/api/v1/admin/payment-events/:id/reprocess` | Retry a failed or unprocessed event |

### Entitlements & Registration

These endpoints provide the first entitlement projection for Walnut clients. Grants use stable entitlement IDs such as `editorial.studio`; product names, VIP copy, subscriptions, and credits should project into grants rather than being checked directly by clients.

`/api/v1/access/login-challenges` is the email login/recovery boundary. The service creates an `AccessLoginChallenge`, stores only an HMAC hash of the OTP, enforces TTL/max-attempt policy, and verifies by consuming the pending challenge before delegating to `AccessSessionService.RegisterOrRestore`. Dev delivery returns `dev_token` so local clients can test without email; production must use a real email delivery adapter and must not expose plaintext tokens.

Login challenge abuse controls are persisted at the identity boundary: challenge creates are limited per normalized email and hashed client IP, failed verify attempts expire the challenge after the configured threshold, and rate-limit/max-attempt events write privacy-safe audit records without raw email, IP, user-agent, device id, or OTP values.

Access session responses include a service-owned `device_capacity` projection with `active_device_count`, `max_devices`, and `remaining_device_slots`. The signed `access_snapshot.device` embeds the same capacity fields so Walnut clients can render restore/device-bind UI without re-deriving slot state; revoked devices are excluded from active capacity and return `access_device_revoked` when reused.

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/access/login-challenges` | `{email, device_id, source, idempotency_key}` | `{challenge_id, email, device_id, expires_at, delivery, dev_token?}` |
| POST | `/api/v1/access/login-challenges/verify` | `{challenge_id, token, device_id, display_name, source}` | Access session with trial/device/device capacity/signed snapshot |
| POST | `/api/v1/access/registrations` | `{email, display_name, device_id, source, note}` | Access session with trial/device/device capacity/signed snapshot |
| POST | `/api/v1/registrations` | `{email, display_name, requested_entitlement, device_id, source, note}` | `{user, registration}` with pending status |
| GET | `/api/v1/users/:user_id/entitlements/snapshot` | — | PC Core compatible entitlement snapshot |

### Legacy Webhooks (called by domestic payment providers)

| Method | Path | Notes |
|--------|------|-------|
| POST | `/api/v1/callbacks/wechat` | WeChat V3 callback (AES-256-GCM encrypted) |
| POST | `/api/v1/callbacks/alipay` | Alipay form callback |

### Admin (API Key auth + route permissions required)

`ADMIN_API_KEYS` remains the local/dev shortcut and maps each key to `admin.*`. Production should prefer `ADMIN_PRINCIPALS_JSON` so each operator key has the minimum permissions required. The embedded `/dashboard` shell asks for an Admin API key; all data and management APIs below are protected by admin middleware.

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/v1/admin/licenses?status=` | List all licenses |
| GET | `/api/v1/admin/licenses/:key` | Single license detail |
| GET | `/api/v1/admin/stats` | Stats: total, active, inactive, expired |
| POST | `/api/v1/admin/licenses/check-expiry` | Deactivate expired subscriptions |
| GET | `/api/v1/admin/access-accounts?email=&status=&limit=` | Masked access-account view for emails registered through `/api/v1/access/registrations` |
| GET | `/api/v1/admin/users/:user_id/access?recent_limit=` | Privacy-safe user troubleshooting summary: devices, trial, grants, subscription projection, recent orders/payment events, risk counts, and cloud quota metadata |
| POST | `/api/v1/admin/devices/:id/revoke` | Revoke one access device; future login/snapshot refresh for that device returns `access_device_revoked` |
| GET | `/api/v1/admin/audit?limit=` | Privacy-projected audit logs; email actors are masked and fingerprinted |
| GET | `/api/v1/admin/registrations?status=` | List legacy entitlement registration requests |
| POST | `/api/v1/admin/registrations/:id/review` | Approve or reject a registration request |
| GET | `/api/v1/admin/grants?user_id=` | List entitlement grants |
| POST | `/api/v1/admin/grants` | Manually grant an entitlement such as `editorial.studio` |
| GET | `/api/v1/admin/orders?user_id=&sku_code=&status=&provider=&order_type=&out_trade_no=&limit=` | Privacy-safe commerce order list with payment-event, fulfillment, and risk diagnostics; no checkout URL or provider subscription/customer IDs |
| GET | `/api/v1/admin/fulfillments?out_trade_no=&user_id=&sku_code=&status=` | List commerce fulfillment executions |
| GET | `/api/v1/admin/payment-risk-flags?user_id=&status=&severity=&provider=&out_trade_no=` | List payment-risk flags for manual review |
| GET | `/api/v1/admin/payment-risk-flags/:id` | Inspect one payment-risk flag |
| POST | `/api/v1/admin/payment-risk-flags/:id/resolve` | Resolve a manual-review checkout hold after operator verification |
| PUT | `/api/v1/admin/payment/creem` | Hot-reload Creem API key, webhook secret, URLs, and SKU→product mapping |

### Infrastructure

| Method | Path | Notes |
|--------|------|-------|
| GET | `/ping` | Liveness probe (returns "pong") |
| GET | `/health` | Readiness probe (checks DB connection) |
| GET | `/metrics` | Prometheus metrics (Go runtime + business metrics) |

## Runbooks

- `docs/RUNBOOK_COMMERCE_FLOW.md`: executable local/test checklist for checkout, webhook inbox, fulfillment, dispute hold, and admin risk resolution.
- `scripts/verify_subscription_control_contract.sh`: local contract for the provider subscription-control port, subscription service, handler errors, and architecture boundaries.
- `scripts/verify_admin_user_access_summary_contract.sh`: local contract for the WCP-4 admin read model, privacy projection, route errors, scoped permission, and architecture boundaries.
- `scripts/verify_admin_order_contract.sh`: local contract for the WCP-4 admin order read model, route errors, scoped permission, and architecture boundaries.

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | 8082 | HTTP listen port |
| `SERVER_ENV` | dev | Environment (dev/prod) |
| `DATABASE_DSN` | ./walnut_billing.db | SQLite database path |
| `ADMIN_API_KEYS` | (empty) | Comma-separated full-access admin API keys; development shortcut that maps to `admin.*` |
| `ADMIN_PRINCIPALS_JSON` | (empty) | Permission-scoped admin keys, e.g. `[{"name":"support","key":"...","permissions":["admin.access_accounts.read","admin.users.read","admin.orders.read","admin.audit.read"]}]`; user access summary requires `admin.users.read`, admin order list requires `admin.orders.read`, device revoke requires `admin.access_accounts.write` |
| `RATELIMIT_ENABLED` | false | Enable IP rate limiting on auth endpoints |
| `PAYMENT_WECHAT_*` | (empty) | Legacy WeChat Pay V3 credentials; not the current commercialization target |
| `PAYMENT_ALIPAY_*` | (empty) | Legacy Alipay credentials; not the current commercialization target |
| `PAYMENT_CREEM_API_KEY` | (empty) | Creem API key; server-side only |
| `PAYMENT_CREEM_WEBHOOK_SECRET` | (empty) | Creem webhook HMAC secret |
| `PAYMENT_CREEM_SANDBOX` | true | Use `https://test-api.creem.io` for checkout and subscription control unless explicitly false |
| `PAYMENT_CREEM_API_BASE_URL` | (empty) | Optional override for Creem API base URL; normally leave empty so sandbox/prod chooses the documented default |
| `PAYMENT_CREEM_SUCCESS_URL` | (empty) | Default hosted checkout success URL; request value can override it |
| `PAYMENT_CREEM_CANCEL_URL` | (empty) | Default hosted checkout cancel URL stored in Walnut metadata |
| `PAYMENT_CREEM_PRODUCT_MAP_JSON` | (empty) | Walnut SKU to Creem product ID map, e.g. `{"pro_own_ai_monthly":"prod_4MS4IC77zjEobSHExt0gcr"}` |
| `PAYMENT_MOCK_CHECKOUT_BASE_URL` | (empty) | Dev-only mock hosted checkout origin; empty means `http://localhost:${SERVER_PORT}` |
| `FULFILLMENT_RULES_JSON` | (empty) | Optional JSON fulfillment rules; empty uses dev defaults |
| `CHECKOUT_RISK_POLICY_ENABLED` | true | Enable pre-checkout risk policy based on Walnut `PaymentRiskFlag` |
| `CHECKOUT_RISK_BLOCK_SEVERITIES` | critical,high | Comma-separated risk severities that require manual review before checkout |
| `ADJUSTMENT_REFUND_WINDOW_DAYS` | 7 | Refund auto-compensation window, counted from Walnut `Order.PaidAt` |
| `ADJUSTMENT_REFUND_IN_WINDOW_ACTION` | auto_refund | Action for in-window refund events: `auto_refund`, `manual_review`, or `reject` |
| `ADJUSTMENT_REFUND_OUT_OF_WINDOW_ACTION` | manual_review | Action for out-of-window refund events |
| `ADJUSTMENT_LOW_USAGE_POLICY_ENABLED` | false | Enable credit-usage based refund decision override |
| `ADJUSTMENT_LOW_USAGE_MAX_CREDITS_USED` | 0 | Max used credits considered low usage when the low-usage policy is enabled |
| `ADJUSTMENT_LOW_USAGE_ACTION` | auto_refund | Action for low-usage in-window refunds |
| `ADJUSTMENT_HIGH_USAGE_ACTION` | manual_review | Action for high-usage in-window refunds |
| `ADJUSTMENT_DISPUTE_ACTION` | auto_refund | Action for dispute/chargeback events; risk flag creation stays enabled |
| `ADJUSTMENT_CANCEL_ACTION` | keep_current_period | Action for cancellation events; default keeps the current paid period |
| `RENEWAL_GRACE_PERIOD_DAYS` | 3 | Grace window after renewal payment failure; grants access only, no period credits |
| `RENEWAL_EXPIRED_ACTION` | expire_grace | Expiry handling: `expire_grace` or `natural_expiry` |
| `ACCESS_SNAPSHOT_*` | dev HS256 values | Signed access snapshot policy and signer configuration |
| `ACCESS_MAX_DEVICES` | 2 | Maximum active devices per access account; projected as device capacity in access sessions and signed snapshots |
| `ACCESS_CLOUD_STORAGE_QUOTA_MB` | 1024 | Default cloud storage quota projected into access snapshots |
| `ACCESS_TRIAL_DURATION_DAYS` | 14 | One-time trial duration keyed by normalized email and trial type |
| `ACCESS_LOGIN_CHALLENGE_TTL_SECONDS` | 600 | Email login/recovery OTP validity window |
| `ACCESS_LOGIN_CHALLENGE_MAX_ATTEMPTS` | 5 | Wrong-token/device attempts before the challenge expires |
| `ACCESS_LOGIN_CHALLENGE_RATE_LIMIT_WINDOW_SECONDS` | 600 | Sliding window for persisted login challenge create limits |
| `ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_EMAIL` | 5 | Max challenge creates per normalized email within the rate-limit window |
| `ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_IP` | 20 | Max challenge creates per hashed client IP within the rate-limit window |
| `ACCESS_LOGIN_CHALLENGE_DELIVERY` | dev | `dev` returns `dev_token` outside prod; `email` is currently disabled until a provider adapter is configured |
| `ACCESS_LOGIN_CHALLENGE_SECRET` | dev secret | HMAC secret for OTP hashing; change in non-dev environments |

Bucket expiry is exposed through `POST /api/v1/admin/credits/buckets/expire` for operator or scheduled jobs.

**Note**: If payment credentials are not configured, the service uses mock adapters (suitable for development).

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `http_requests_total` | Counter | Total HTTP requests by method, path, status |
| `http_request_duration_seconds` | Histogram | Request latency |
| `commerce_checkouts_total` | Counter | Checkout attempts by provider, SKU, status, error kind |
| `commerce_checkout_duration_seconds` | Histogram | Checkout orchestration latency by provider, SKU, status |
| `checkout_policy_blocks_total` | Counter | Checkout holds by policy reason and action, including payment-risk manual review |
| `payment_events_total` | Counter | Webhook receive/reprocess attempts by provider, event type, inbox status, error kind |
| `payment_event_duration_seconds` | Histogram | Webhook receive/reprocess latency by provider, event type, inbox status |
| `fulfillments_total` | Counter | Fulfillment attempts by SKU, order type, status, error kind |
| `fulfillment_duration_seconds` | Histogram | Fulfillment latency by SKU, order type, status |
| `payment_adjustments_total` | Counter | Refund/dispute/cancel adjustment attempts by event type, status, policy action, error kind |
| `payment_adjustment_duration_seconds` | Histogram | Adjustment policy latency by event type, status, policy action |
| `orders_created_total` | Counter | Total legacy orders created |
| `license_activations_total` | Counter | Total license activations |

## Seed Products

| Code | Name | Price | Validity |
|------|------|-------|----------|
| pro | walnut Pro (Buyout, legacy) | ¥128 | Lifetime |
| std | walnut Standard (Buyout, legacy) | ¥68 | Lifetime |
| sub_monthly | AI Subscription (Monthly, legacy) | ¥15/month | Monthly |
| sub_yearly | AI Subscription (Yearly, legacy) | ¥150/year | Yearly |
| pro_own_ai_monthly | Walnut Pro Own AI Monthly | $5/month | Monthly |
| pro_own_ai_lifetime | Walnut Pro Own AI Lifetime | $99 | Lifetime |
| editorial_studio_monthly | Editorial Studio Monthly (legacy hidden) | $19/month | Monthly |
| credits_600 | Walnut Credits 600 (legacy hidden) | $9.9 | Lifetime |

## Design Patterns

| Pattern | Application |
|---------|-------------|
| Factory | License key generation (product-specific formats) |
| Adapter | Creem hosted checkout, webhook verifier, and subscription control; legacy WeChat/Alipay remain behind the same interface |
| Facade | Commerce checkout entry point hides provider details from PC/mobile clients |
| Repository | Data access abstraction (interface → GORM implementation) |
| UnitOfWork | Database transactions for license creation and commerce fulfillment |
| Middleware Chain | Recovery → RequestID → Logger → Prometheus → Auth |
| EntitlementService | Registration, manual grant, and snapshot projection facade |
| CheckoutService | Provider-agnostic order + checkout-session orchestration |
| PaymentEventService | Webhook inbox, provider event idempotency, retries, and reprocessing |
| FulfillmentService | Rule-engine facade for paid order delivery into grants and credit ledger rows |
| SubscriptionRenewalService | Provider-agnostic renewal/grace policy executor |
| SubscriptionControlProvider | Optional provider port for cancel/resume; payment adapters own test/prod URL and payload translation |
| Strategy | Fulfillment rule executors, payment adjustment policy, and subscription renewal policy |
| Catalog | Validates stable entitlement IDs and configurable fulfillment rules independently from provider copy |
| Bucket FEFO | Credit bucket allocation and expiry prioritize earliest expiring credits first |

## License

Proprietary. All rights reserved.
