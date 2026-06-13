# walnut Billing Server

License and billing microservice for walnut products. Single-binary Go service with SQLite backend.

## Architecture

```
walnut-billing/
├── cmd/server/main.go              # Entry point (DI + server start)
├── internal/
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

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/commerce/checkout-sessions` | `{user_id, sku_code, provider, success_url, cancel_url, idempotency_key}` | `{order, checkout_url, provider}` |

Development builds register a `mock` checkout provider. Creem can be enabled as a hosted checkout adapter through `PAYMENT_CREEM_*`; PC/mobile clients still call the same Walnut checkout facade and never depend on Creem IDs.

### Commerce Fulfillment

Paid commerce orders are fulfilled through Walnut-owned rules, not through provider state. `FulfillmentService` reads the paid order SKU, executes configured rules, writes `FulfillmentExecution` rows for idempotency/audit, and grants only stable Walnut targets such as `EntitlementGrant` and `CreditTransaction`.

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/v1/admin/fulfillments?out_trade_no=&user_id=&sku_code=&status=` | List fulfillment executions for audit/reprocess diagnostics |

Fulfillment rules can be supplied through `FULFILLMENT_RULES_JSON`; the dev defaults include `editorial_studio_monthly` and `credits_600`. The production path uses UnitOfWork so order status, entitlement grants, credit ledger rows, and fulfillment executions converge safely under retries.

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

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/registrations` | `{email, display_name, requested_entitlement, device_id, source, note}` | `{user, registration}` with pending status |
| GET | `/api/v1/users/:user_id/entitlements/snapshot` | — | PC Core compatible entitlement snapshot |

### Legacy Webhooks (called by domestic payment providers)

| Method | Path | Notes |
|--------|------|-------|
| POST | `/api/v1/callbacks/wechat` | WeChat V3 callback (AES-256-GCM encrypted) |
| POST | `/api/v1/callbacks/alipay` | Alipay form callback |

### Admin (API Key auth required)

| Method | Path | Notes |
|--------|------|-------|
| GET | `/api/v1/admin/licenses?status=` | List all licenses |
| GET | `/api/v1/admin/licenses/:key` | Single license detail |
| GET | `/api/v1/admin/stats` | Stats: total, active, inactive, expired |
| POST | `/api/v1/admin/licenses/check-expiry` | Deactivate expired subscriptions |
| GET | `/api/v1/admin/registrations?status=` | List entitlement registration requests |
| POST | `/api/v1/admin/registrations/:id/review` | Approve or reject a registration request |
| GET | `/api/v1/admin/grants?user_id=` | List entitlement grants |
| POST | `/api/v1/admin/grants` | Manually grant an entitlement such as `editorial.studio` |
| GET | `/api/v1/admin/fulfillments?out_trade_no=&user_id=&sku_code=&status=` | List commerce fulfillment executions |
| PUT | `/api/v1/admin/payment/creem` | Hot-reload Creem API key, webhook secret, URLs, and SKU→product mapping |

### Infrastructure

| Method | Path | Notes |
|--------|------|-------|
| GET | `/ping` | Liveness probe (returns "pong") |
| GET | `/health` | Readiness probe (checks DB connection) |
| GET | `/metrics` | Prometheus metrics (Go runtime + business metrics) |

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | 8082 | HTTP listen port |
| `SERVER_ENV` | dev | Environment (dev/prod) |
| `DATABASE_DSN` | ./walnut_billing.db | SQLite database path |
| `ADMIN_API_KEYS` | (empty) | Comma-separated API keys for admin endpoints |
| `RATELIMIT_ENABLED` | false | Enable IP rate limiting on auth endpoints |
| `PAYMENT_WECHAT_*` | (empty) | Legacy WeChat Pay V3 credentials; not the current commercialization target |
| `PAYMENT_ALIPAY_*` | (empty) | Legacy Alipay credentials; not the current commercialization target |
| `PAYMENT_CREEM_API_KEY` | (empty) | Creem API key; server-side only |
| `PAYMENT_CREEM_WEBHOOK_SECRET` | (empty) | Creem webhook HMAC secret |
| `PAYMENT_CREEM_SANDBOX` | true | Use `https://test-api.creem.io` unless explicitly false |
| `PAYMENT_CREEM_API_BASE_URL` | (empty) | Optional override for Creem API base URL; normally leave empty |
| `PAYMENT_CREEM_SUCCESS_URL` | (empty) | Default hosted checkout success URL; request value can override it |
| `PAYMENT_CREEM_CANCEL_URL` | (empty) | Default hosted checkout cancel URL stored in Walnut metadata |
| `PAYMENT_CREEM_PRODUCT_MAP_JSON` | (empty) | SKU to Creem product ID map, e.g. `{"editorial_studio_monthly":"prod_xxx"}` |
| `FULFILLMENT_RULES_JSON` | (empty) | Optional JSON fulfillment rules; empty uses dev defaults |
| `CHECKOUT_RISK_POLICY_ENABLED` | true | Enable pre-checkout risk policy based on Walnut `PaymentRiskFlag` |
| `CHECKOUT_RISK_BLOCK_SEVERITIES` | critical,high | Comma-separated risk severities that require manual review before checkout |

**Note**: If payment credentials are not configured, the service uses mock adapters (suitable for development).

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `http_requests_total` | Counter | Total HTTP requests by method, path, status |
| `http_request_duration_seconds` | Histogram | Request latency |
| `orders_created_total` | Counter | Total orders created |
| `license_activations_total` | Counter | Total license activations |

## Seed Products

| Code | Name | Price | Validity |
|------|------|-------|----------|
| pro | walnut Pro (Buyout, legacy) | ¥128 | Lifetime |
| std | walnut Standard (Buyout, legacy) | ¥68 | Lifetime |
| sub_monthly | AI Subscription (Monthly, legacy) | ¥15/month | Monthly |
| sub_yearly | AI Subscription (Yearly, legacy) | ¥150/year | Yearly |
| editorial_studio_monthly | Editorial Studio Monthly | $19/month | Monthly |
| credits_600 | Walnut Credits 600 | $9.9 | Lifetime |

## Design Patterns

| Pattern | Application |
|---------|-------------|
| Factory | License key generation (product-specific formats) |
| Adapter | Creem hosted checkout and webhook verifier; legacy WeChat/Alipay remain behind the same interface |
| Facade | Commerce checkout entry point hides provider details from PC/mobile clients |
| Repository | Data access abstraction (interface → GORM implementation) |
| UnitOfWork | Database transactions for license creation and commerce fulfillment |
| Middleware Chain | Recovery → RequestID → Logger → Prometheus → Auth |
| EntitlementService | Registration, manual grant, and snapshot projection facade |
| CheckoutService | Provider-agnostic order + checkout-session orchestration |
| PaymentEventService | Webhook inbox, provider event idempotency, retries, and reprocessing |
| FulfillmentService | Rule-engine facade for paid order delivery into grants and credit ledger rows |
| Strategy | Fulfillment rule executors for entitlement and credits targets |
| Catalog | Validates stable entitlement IDs and configurable fulfillment rules independently from provider copy |

## License

Proprietary. All rights reserved.
