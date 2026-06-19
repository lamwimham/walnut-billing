# ADR: Cloud Storage Provider Strategy

Status: Accepted for first implementation target
Date: 2026-06-19

## Context

`walnut-billing` owns the cloud-storage control plane: entitlement checks, plan-aware quota, upload authorization sessions, manifest commits, object metadata, restore metadata, client download-target authorization, and admin read models. It must not proxy file bytes or store Wiki/material content in the billing database.

The provider decision must keep Walnut App independent from storage-vendor details. Clients call billing for upload/restore metadata; object bytes move directly between the client and the storage provider using short-lived targets.

## Decision

The first real provider target is an S3-compatible adapter, with Cloudflare R2 as the preferred hosted deployment. The adapter lives in `internal/objectstorage` and is wired only from bootstrap; cloud-storage services continue to depend on the provider-neutral `ObjectStorageProvider` port.

- `BuildUploadTarget`
- `BuildDownloadTarget`
- `HeadObject`
- `DeleteObject`
- lifecycle tags on upload requests

`CLOUD_STORAGE_PROVIDER=r2` or `s3` enables the S3-compatible SigV4 adapter. It generates short-lived presigned upload/download URLs and uses signed server-side HEAD/DELETE requests for diagnostics or future cleanup workflows. Until the adapter is configured, production keeps `UnconfiguredObjectStorageProvider`, and cloud sync APIs fail with stable `cloud storage provider not configured` semantics. Software licensing, checkout, subscriptions, and access snapshots must continue to work without object storage.

The implementation signs requests directly instead of importing a large SDK. This keeps the adapter deterministic, testable without network access, and limited to the exact S3 surface used by the billing control plane.

## Options Considered

| Option | Pros | Cons | Decision |
|---|---|---|---|
| S3-compatible / Cloudflare R2 | Broad ecosystem, direct upload/download target model, good future portability, simple lifecycle tagging | Need careful key/metadata policy and region/account config | First implementation target |
| Alibaba OSS | Strong mainland China option, mature signed URL support | More region/compliance decisions; weaker portability for global launch | Defer until China-specific deployment is needed |
| Self-hosted MinIO | Maximum control, S3-compatible development path | Adds availability, backup, upgrade, and security operations burden | Use only for local/dev or special self-hosted deployments |
| Managed app storage platforms | Fastest prototype path | Vendor lock-in and weaker control over lifecycle/object metadata | Not a billing control-plane default |

## Control-Plane Invariants

- Billing never receives or stores object bytes.
- `CloudSyncSession` must be authorized before `CloudManifest` can be committed.
- A manifest commit must match the authorized session resource fingerprint and provider.
- Restore metadata APIs return project, latest manifest, and object metadata only.
- Object download targets are authorized through `POST /api/v1/cloud-storage/download-targets`, which checks user/project/object ownership before delegating to `ObjectStorageProvider.BuildDownloadTarget`.
- Object keys are provider-neutral and derived from stable Walnut identifiers, resource kind, content hash, and sanitized filename; never from local absolute paths.
- Storage credentials stay in server config and never appear in API responses, logs, metrics, access snapshots, or admin read models.
- Admin cloud read models never expose object keys, upload/download URLs, local paths, provider object ids, or file contents.
- Cloud quota is decided by a shared `CloudStorageQuotaPolicy` strategy so client usage, access snapshots, and admin read models project the same trial/monthly/lifetime/custom plan.

## Consequences

- The current code can safely ship with or without real object storage configured because unconfigured provider errors are explicit and isolated to cloud sync.
- Adding another S3-compatible target should be config-only unless the provider deviates from SigV4/presigned URL behavior.
- Plan-aware quota changes remain service-policy decisions and do not require provider adapter changes.
