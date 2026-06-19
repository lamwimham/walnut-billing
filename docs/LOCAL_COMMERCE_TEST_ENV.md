# Local Commerce Test Environment

This runbook gives Walnut a repeatable environment for testing the production-like access loop without touching personal billing data.

## What This Environment Tests

```
Walnut Core -> walnut-billing -> hosted checkout provider -> webhook/fulfillment -> signed access snapshot -> Walnut Core refresh
```

Use two profiles:

- **Mock hosted checkout**: deterministic, no external network, suitable for repeated regression tests.
- **Creem test mode**: real hosted checkout and webhook contract, but sandbox products and test payments.

## 1. Start Isolated walnut-billing

```bash
cd ../walnut-billing
scripts/reset_local_billing_test_env.sh
scripts/run_local_billing_test_env.sh
```

Defaults:

- Billing URL: `http://127.0.0.1:8082`
- SQLite DB: `../walnut-billing/.tmp/local-commerce/data/walnut_billing_test.db`
- Admin key: `local-admin-key`
- Mock checkout URL: `http://localhost:8082/checkout/...`

Use a new home for each scenario when you need clean state:

```bash
WALNUT_BILLING_TEST_HOME=/tmp/walnut-billing-case-001 scripts/run_local_billing_test_env.sh
```


## 1A. Deterministic Mock Profile

For repeatable local verification, use the checked-in deterministic profile:

```bash
cd ../walnut-billing
scripts/reset_deterministic_billing.sh
scripts/run_deterministic_billing.sh
```

This profile uses:

- config file: `config/local.deterministic.env`
- billing URL: `http://127.0.0.1:8082`
- dashboard: `http://127.0.0.1:8082/dashboard`
- SQLite DB: `.tmp/deterministic-billing/data/walnut_billing_deterministic.db`
- dev admin key: `local-admin-key`
- scoped read-only key: `support-key`
- scoped full-ops key: `ops-key`
- mock checkout URL: `http://127.0.0.1:8082/checkout/...`

Use one-off overrides only when needed, for example `SERVER_PORT=8083 scripts/run_deterministic_billing.sh`.

## 1B. Email Login / Recovery Challenge

The deterministic profile uses `ACCESS_LOGIN_CHALLENGE_DELIVERY=dev`, so challenge creation returns a `dev_token`. This is only for local/test verification; production disables dev delivery until a real email provider adapter is configured.

```bash
BASE_URL=http://127.0.0.1:8082
EMAIL=recovery+001@example.com
DEVICE_ID=device-recovery-1

CHALLENGE_RESPONSE=$(curl -sS -X POST "$BASE_URL/api/v1/access/login-challenges" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"device_id\":\"$DEVICE_ID\",\"source\":\"desktop\",\"idempotency_key\":\"login:$EMAIL:$DEVICE_ID\"}")

CHALLENGE_ID=$(printf '%s' "$CHALLENGE_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["challenge_id"])')
TOKEN=$(printf '%s' "$CHALLENGE_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("dev_token", ""))')

curl -sS -X POST "$BASE_URL/api/v1/access/login-challenges/verify" \
  -H 'Content-Type: application/json' \
  -d "{\"challenge_id\":\"$CHALLENGE_ID\",\"token\":\"$TOKEN\",\"device_id\":\"$DEVICE_ID\",\"display_name\":\"Recovery Tester\"}" | python3 -m json.tool
```

Expected behavior:

- `AccessLoginChallenge.token_hash` is persisted, but no plaintext token is stored.
- Client IP and User-Agent are stored as hashes only; raw IP/User-Agent do not enter the challenge row or abuse audit details.
- Verification consumes the challenge exactly once and delegates to the existing access session service, so trial idempotency, device limits, device capacity, and signed snapshot issuance stay in one place.
- The verify response contains `device_capacity.remaining_device_slots`; the signed `access_snapshot.device` contains the same remaining-slot projection for offline restore UI.
- Reusing a consumed/expired challenge returns a stable error code such as `login_challenge_failed` or `login_challenge_expired`.
- Excess challenge creation returns `login_challenge_rate_limited` and records `access.login_challenge.abuse` with only fingerprints/hashes.

The contract test for this slice is:

```bash
scripts/verify_access_login_challenge_contract.sh
```

## 2. Start Walnut Core Against Billing

From `sagemate-core`:

```bash
WALNUT_DATA_DIR=/tmp/walnut-core-commerce-test \
WALNUT_ACCESS_CONTROL_MODE=billing \
WALNUT_ACCESS_BILLING_BASE_URL=http://127.0.0.1:8082 \
WALNUT_ACCESS_CHECKOUT_PROVIDER=mock \
WALNUT_ACCESS_SNAPSHOT_SECRET=walnut-dev-access-snapshot-secret \
WALNUT_ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM=HS256 \
WALNUT_ACCESS_SNAPSHOT_KEY_ID=dev \
.venv/bin/sagemate --host 127.0.0.1 --port 8000
```

## 3. Verify The Full Mock Checkout Loop

```bash
python scripts/verify_billing_checkout_e2e.py \
  --core-url http://127.0.0.1:8000 \
  --billing-url http://127.0.0.1:8082 \
  --admin-key local-admin-key \
  --email checkout-e2e+001@example.com \
  --sku pro_own_ai_monthly
```

Repeat with a different email or reset the test home to start from scratch.

## 4. Admin Views

- Dashboard: `http://127.0.0.1:8082/dashboard`
- Masked access accounts API:

```bash
curl -H 'Authorization: Bearer local-admin-key' \
  'http://127.0.0.1:8082/api/v1/admin/access-accounts?limit=20' | python3 -m json.tool
```

The access-account API intentionally returns `email_masked`, `email_domain`, and `email_fingerprint`; it does not return raw emails. For a single user, the WCP-4 troubleshooting view aggregates the same privacy boundary with commerce/control-plane facts:

```bash
USER_ID=$(curl -sS -H 'Authorization: Bearer local-admin-key' \
  'http://127.0.0.1:8082/api/v1/admin/access-accounts?limit=1' \
  | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data["accounts"][0]["user_id"])')

curl -sS -H 'Authorization: Bearer local-admin-key' \
  "http://127.0.0.1:8082/api/v1/admin/users/$USER_ID/access?recent_limit=10" | python3 -m json.tool

curl -sS -H 'Authorization: Bearer local-admin-key' \
  "http://127.0.0.1:8082/api/v1/admin/orders?user_id=$USER_ID&limit=10" | python3 -m json.tool
```

`/api/v1/admin/users/:user_id/access` returns device capacity, current trial/grants, `SoftwareSubscriptionProjector` status, recent order summaries, payment-event `payload_hash` only, risk counters, and cloud quota metadata. It deliberately does not return raw email, raw device id, checkout URL, provider subscription id, provider event id, or webhook raw payload. Audit logs are also projected through a privacy boundary: historical raw-email actors are returned as masked email + stable fingerprint, and new access-registration audit entries use `user_id` as actor.

`/api/v1/admin/orders` is the commerce-side companion read model. It can filter by `user_id`, `sku_code`, `status`, `provider`, `order_type`, or `out_trade_no`, and returns provider-neutral diagnostics: payment-event count/latest `payload_hash`, fulfillment count/failures, and open risk count. It only exposes boolean presence flags for checkout session, provider customer, and provider subscription metadata.

For closer-to-production permission testing, replace the legacy full-access key with scoped principals:

```bash
export ADMIN_API_KEYS=
export ADMIN_PRINCIPALS_JSON='[{"name":"support","key":"support-key","permissions":["admin.dashboard.read","admin.access_accounts.read","admin.users.read","admin.orders.read","admin.audit.read"]},{"name":"ops","key":"ops-key","permissions":["admin.*"]}]'
```

Use `support-key` to verify read-only views and `ops-key` to verify management actions such as grant creation, webhook reprocessing, and risk resolution.

Device lifecycle check:

```bash
# First identify a device id from the privacy-safe access account projection.
DEVICE_ROW_ID=$(curl -sS -H 'Authorization: Bearer ops-key' \
  'http://127.0.0.1:8082/api/v1/admin/access-accounts?limit=1' \
  | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data["accounts"][0]["devices"][0]["id"])')

curl -sS -X POST "http://127.0.0.1:8082/api/v1/admin/devices/$DEVICE_ROW_ID/revoke" \
  -H 'Authorization: Bearer ops-key' \
  -H 'Content-Type: application/json' \
  -d '{"revoked_by":"ops","reason":"local lifecycle test"}' | python3 -m json.tool
```

After revoke, the same device cannot restore or refresh a signed access snapshot; APIs return `access_device_revoked`. Revoked devices are excluded from `active_device_count`, so remaining slots reopen only through the service-owned capacity projection. Run the contract test with:

```bash
scripts/verify_access_device_lifecycle_contract.sh
scripts/verify_admin_user_access_summary_contract.sh
scripts/verify_admin_order_contract.sh
```

## 5. Creem Test Mode Profile

Creem test mode must stay isolated from production:

- Test API base URL: `https://test-api.creem.io` (leave `PAYMENT_CREEM_API_BASE_URL` empty when `PAYMENT_CREEM_SANDBOX=true`).
- Test and production API keys are separate; never use a `creem_live...` key with sandbox mode or a `creem_test...` key with production mode.
- Test products are separate from production products. Map every checkout-visible Walnut SKU before enabling Creem (`pro_own_ai_monthly` and `pro_own_ai_lifetime` today).
- Test webhooks must point to the test webhook URL in the Creem dashboard; production webhooks use a different endpoint/secret.
- Subscription control uses the same documented paths in test and prod: `POST /v1/subscriptions/{id}/cancel` and `POST /v1/subscriptions/{id}/resume`; switching production is a config change from `PAYMENT_CREEM_SANDBOX=true` to `false` with production credentials.

First run the local adapter/fixture contract; it does not call Creem:

```bash
scripts/verify_creem_sandbox_contract.sh
scripts/verify_subscription_control_contract.sh
```

Set these before `scripts/run_local_billing_test_env.sh`:

```bash
export PAYMENT_CREEM_API_KEY='creem_test_...'
export PAYMENT_CREEM_WEBHOOK_SECRET='whsec_...'
export PAYMENT_CREEM_SANDBOX=true
export PAYMENT_CREEM_API_BASE_URL=
export PAYMENT_CREEM_PRODUCT_MAP_JSON='{"pro_own_ai_monthly":"prod_test_monthly","pro_own_ai_lifetime":"prod_test_lifetime"}'
export PAYMENT_CREEM_SUCCESS_URL='walnut://checkout/success'
export PAYMENT_CREEM_CANCEL_URL='walnut://checkout/cancel'
```

Then start Walnut Core with:

```bash
WALNUT_ACCESS_CHECKOUT_PROVIDER=creem
```

For local webhook delivery, expose billing with `ngrok`/`cloudflared` and point the Creem test-mode dashboard webhook to:

```text
https://<public-test-host>/api/v1/webhooks/creem
```

Use Creem's successful test card `4242 4242 4242 4242` for paid checkout validation; keep mock checkout as the deterministic CI/local regression path. The adapter refuses obvious sandbox/production endpoint or key mixing before registering the provider.

For monthly subscription cancel/resume validation, ensure the paid checkout or renewal webhook contains a provider subscription id. Billing records it as `walnut_provider_subscription_id` on the Walnut order, then cancel/resume calls Creem first and only writes Walnut cancellation/resume facts after provider success. Provider failures return stable codes (`subscription_control_unavailable` or `subscription_control_failed`) and can be retried with the same idempotency key.

The admin provider status endpoint also reports unavailable Creem state:

```bash
curl -H 'Authorization: Bearer local-admin-key' \
  'http://127.0.0.1:8082/api/v1/admin/payment/providers' | python3 -m json.tool
```

Expected states:

- `active`: Creem checkout/webhook adapter is registered.
- `disabled`: no Creem credentials/product map were provided; mock flow can still run.
- `error`: some Creem settings exist but are incomplete, mixed between test/prod, or missing required SKU mappings.
