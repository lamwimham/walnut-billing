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

The access-account API intentionally returns `email_masked`, `email_domain`, and `email_fingerprint`; it does not return raw emails. Audit logs are also projected through a privacy boundary: historical raw-email actors are returned as masked email + stable fingerprint, and new access-registration audit entries use `user_id` as actor.

For closer-to-production permission testing, replace the legacy full-access key with scoped principals:

```bash
export ADMIN_API_KEYS=
export ADMIN_PRINCIPALS_JSON='[{"name":"support","key":"support-key","permissions":["admin.dashboard.read","admin.access_accounts.read","admin.audit.read"]},{"name":"ops","key":"ops-key","permissions":["admin.*"]}]'
```

Use `support-key` to verify read-only views and `ops-key` to verify management actions such as grant creation, webhook reprocessing, and risk resolution.

## 5. Creem Test Mode Profile

Creem test mode must stay isolated from production:

- Test API base URL: `https://test-api.creem.io` (leave `PAYMENT_CREEM_API_BASE_URL` empty when `PAYMENT_CREEM_SANDBOX=true`).
- Test and production API keys are separate; never use a `creem_live...` key with sandbox mode or a `creem_test...` key with production mode.
- Test products are separate from production products. Map every checkout-visible Walnut SKU before enabling Creem (`pro_own_ai_monthly` and `pro_own_ai_lifetime` today).
- Test webhooks must point to the test webhook URL in the Creem dashboard; production webhooks use a different endpoint/secret.

First run the local adapter/fixture contract; it does not call Creem:

```bash
scripts/verify_creem_sandbox_contract.sh
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

The admin provider status endpoint also reports unavailable Creem state:

```bash
curl -H 'Authorization: Bearer local-admin-key' \
  'http://127.0.0.1:8082/api/v1/admin/payment/providers' | python3 -m json.tool
```

Expected states:

- `active`: Creem checkout/webhook adapter is registered.
- `disabled`: no Creem credentials/product map were provided; mock flow can still run.
- `error`: some Creem settings exist but are incomplete, mixed between test/prod, or missing required SKU mappings.
