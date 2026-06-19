# Cloud Storage Control Plane Runbook

This runbook closes the first WCP-5 control-plane slice. Cloud storage is metadata-first: `walnut-billing` authorizes sync, tracks quota, records manifests and object metadata, and exposes restore metadata. Object bytes remain outside billing.

## Architecture

```text
Walnut App / PC Core
  -> POST /api/v1/cloud-storage/sync-sessions
    -> entitlement + quota check
    -> ObjectStorageProvider.BuildUploadTarget
    -> CloudSyncSession(authorized, expires_at, resource_fingerprint)
  -> direct upload to object storage
  -> POST /api/v1/cloud-storage/manifests
    -> validate CloudSyncSession + resource fingerprint
    -> commit CloudManifest + CloudObject metadata
  -> GET restore metadata
```

Handlers remain transport-only. `CloudStorageService` owns authorization/session/manifest state. Provider-specific storage code belongs behind `ObjectStorageProvider`; billing must not parse file content or proxy bytes.

## Client Flow

1. Ensure the user has `cloud.storage` entitlement in the signed access snapshot.
2. Call `POST /api/v1/cloud-storage/sync-sessions` with `user_id`, `client_project_id`, project name, and resource descriptors.
3. Upload each resource directly to the returned provider upload target before `expires_at`.
4. Call `POST /api/v1/cloud-storage/manifests` with the returned `sync_session_id`, manifest hash, manifest version, same resource descriptors, and an idempotency key.
5. For restore on a new device, call:
   - `GET /api/v1/users/:user_id/cloud-storage/projects`
   - `GET /api/v1/cloud-storage/projects/:project_id/manifests/latest?user_id=:user_id`

## Error Semantics

| Error | HTTP | Meaning | Action |
|---|---:|---|---|
| `cloud storage provider not configured` | 409 | No real object storage adapter is configured | Keep software access working; disable sync UI or show maintenance |
| `cloud storage access denied` | 403 | Missing cloud entitlement, inactive user, or zero quota | Refresh access snapshot or route to upgrade/support |
| `cloud storage quota exceeded` | 402 | Projected active object bytes exceed quota | Show used/quota/requested; ask user to clean up or upgrade |
| `cloud sync session not found` | 404 | Manifest commit references an unknown session | Re-authorize sync and upload again |
| `cloud sync session expired` | 409 | Upload authorization expired before commit | Re-authorize sync; do not reuse old upload targets |
| `cloud sync session already committed` | 409 | Session was already consumed by a manifest | Use idempotency replay for the same commit or create a new session |

## Restore Metadata Contract

Restore APIs return only metadata needed to rebuild local file lists:

- project id, client project id, project name, status, latest manifest id
- manifest id/hash/version/object count/bytes/status
- active object resource id, resource kind, provider-neutral object key, content hash, size, content type

They do not return file bytes, local absolute paths, upload/download URLs, provider object ids, secrets, or admin-only masked identity data.

## Operator Checks

- Use `GET /api/v1/admin/cloud-storage/usage` to inspect usage rollups.
- Use `GET /api/v1/admin/users/:user_id/cloud-storage/projects` for metadata-only project summaries.
- Use `cloud_sync_total{error_kind="over_quota"}` and `cloud_sync_total{error_kind="provider_not_configured"}` for alert triage.
- Do not inspect object bytes during billing support. If a provider issue is suspected, compare metadata counts, manifest hashes, and provider head-object diagnostics through the future adapter tooling.

## Verification

Run:

```bash
scripts/verify_cloud_storage_control_contract.sh
go test ./...
git diff --check
```
