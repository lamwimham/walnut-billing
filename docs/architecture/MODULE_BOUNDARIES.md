# Walnut Billing Module Boundaries

`walnut-billing` is the Walnut commercial control plane. It is currently a modular monolith: code can still live in existing layered packages, but every endpoint and service must have a clear module owner before more physical package moves happen.

## Request Flow

```text
cmd/server/main.go
  -> internal/app/bootstrap.Build
    -> database migration runner
    -> repositories + platform adapters + application services
    -> module route registrars
      -> api handlers
        -> service interfaces
          -> repository and provider ports
```

`cmd/server/main.go` only owns process startup and graceful shutdown. Bootstrap owns wiring. Module registrars in `internal/app/bootstrap/router.go` own route placement and make the intended bounded context visible.

## Logical Modules

| Module | Route owner | Responsibilities | Must not depend on |
|---|---|---|---|
| `database_migration` | `internal/app/migration` | Versioned schema changes, migration metadata, dev auto-migrate compatibility | HTTP handlers, payment providers, business policies |
| `database_backup` | `scripts/backup_sqlite.sh`, `scripts/verify_sqlite_restore.sh` | Operational DB snapshots, checksum, restore drills | Application services or HTTP request flow |
| `identity/access` | `identityAccessModule` | Email registration/restore, device-bound access snapshots, entitlement grants, credit ledger endpoints | payment provider details, checkout URLs, object storage implementation |
| `commerce` | `commerceModule` | Checkout facade, provider webhook inbox, fulfillment diagnostics, subscription actions, payment-risk operations | Walnut App UI gates, cloud object bytes, direct snapshot cache writes |
| `cloud_storage` | `cloudStorageModule` | Cloud quota checks, sync sessions, manifest/object metadata | payment providers, commerce checkout policy, file content parsing |
| `admin` | `adminModule` plus admin sections of other modules | RBAC-protected operator APIs, audit views, manual operations through services | direct DB writes that bypass service/audit boundaries |
| `legacy_license` | `legacyLicenseModule` | Existing key/license/order compatibility APIs while commerce checkout becomes primary | new commercial feature gates |
| `infrastructure` | `registerInfrastructureRoutes` | Health, metrics, dashboard shell, dev mock checkout pages | business decisions |

## Dependency Rules

```text
api/handler
  -> service application interfaces
    -> repository interfaces + platform adapter interfaces
      -> gorm_repo / payment / future object storage adapters
```

Architecture tests enforce the first hard rules:

- `internal/service` must not import `internal/api` or `internal/api/handler`.
- Access-oriented services (`access_*.go`, `entitlement.go`, `credit*.go`) must not import `internal/payment`.
- Cloud storage services must not import `internal/payment`.
- API handlers must not import `internal/repository/gorm_repo`; persistence access goes through services.
- Domain models must not import service, api, payment, or repository packages.

## Extension Guidance

- New client APIs must be registered under the module that owns their state transition.
- New provider-specific behavior belongs behind an adapter interface, not in handlers or access services.
- HTTP browser-boundary policy (CORS, HSTS, CSP, frame/referrer/permissions headers) belongs in `internal/api/middleware` and `internal/config`, not in business handlers or services.
- Database schema evolution belongs in `internal/app/migration`; bootstrap may select a migration mode but must not own table lists or ad-hoc schema changes.
- Backup/restore automation belongs in scripts and runbooks; runtime business services should not create backups or restore databases.
- Provider IDs, product IDs, and payment statuses must not enter access snapshots or Walnut App feature gates.
- Admin write actions must go through application services and audit the principal, target, reason, and outcome.
