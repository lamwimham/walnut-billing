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
│   ├── payment/                    # Adapter pattern: WeChat Pay + Alipay
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

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/orders` | `{product_code}` | `{order: {out_trade_no, license_key, amount}}` |
| POST | `/api/v1/orders/pay` | `{out_trade_no, provider}` | `{payment_url}` |
| GET | `/api/v1/orders/:out_trade_no` | — | Order status + payment info |


### Entitlements & Registration

These endpoints provide the first entitlement projection for Walnut clients. Grants use stable entitlement IDs such as `editorial.studio`; product names, VIP copy, subscriptions, and credits should project into grants rather than being checked directly by clients.

| Method | Path | Request | Response |
|--------|------|---------|----------|
| POST | `/api/v1/registrations` | `{email, display_name, requested_entitlement, device_id, source, note}` | `{user, registration}` with pending status |
| GET | `/api/v1/users/:user_id/entitlements/snapshot` | — | PC Core compatible entitlement snapshot |

### Webhooks (called by payment providers)

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
| `PAYMENT_WECHAT_*` | (empty) | WeChat Pay V3 credentials |
| `PAYMENT_ALIPAY_*` | (empty) | Alipay credentials |

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
| pro | walnut Pro (Buyout) | ¥128 | Lifetime |
| std | walnut Standard (Buyout) | ¥68 | Lifetime |
| sub_monthly | AI Subscription (Monthly) | ¥15/month | Monthly |
| sub_yearly | AI Subscription (Yearly) | ¥150/year | Yearly |

## Design Patterns

| Pattern | Application |
|---------|-------------|
| Factory | License key generation (product-specific formats) |
| Adapter | Payment gateways (WeChat V3 / Alipay unified interface) |
| Repository | Data access abstraction (interface → GORM implementation) |
| UnitOfWork | Database transactions (atomic Order + License creation) |
| Middleware Chain | Recovery → RequestID → Logger → Prometheus → Auth |
| EntitlementService | Registration, manual grant, and snapshot projection facade |
| Catalog | Validates stable entitlement IDs independently from products and VIP copy |

## License

Proprietary. All rights reserved.
