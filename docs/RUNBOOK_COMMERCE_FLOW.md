# Commerce Flow Runbook

这份运行手册把当前 `walnut-billing` 的海外 hosted checkout 商业化闭环固化成可执行的本地/测试环境验收流程。当前海外渠道以 Creem 为第一实现，但 Walnut 的客户端、门禁和权益判断只能依赖 Walnut 自有事实，不能依赖 Creem 字段或支付平台状态。

## 范围

本手册覆盖：

- 创建或识别 Walnut 用户。
- 通过 `/api/v1/commerce/checkout-sessions` 创建 checkout session。
- 通过 `/api/v1/webhooks/:provider` 接收 paid webhook。
- 验证 `Order -> PaymentEventInbox -> FulfillmentExecution -> EntitlementGrant -> EntitlementSnapshot`；legacy credit SKU 另验 `CreditTransaction/CreditBucket`。
- 验证重复 webhook 幂等。
- 模拟 dispute/chargeback，完成 revoke/clawback、创建 `PaymentRiskFlag`、阻断新 checkout、人工 resolve 后恢复 checkout。
- 模拟订阅续费成功、续费失败 3 天 grace period、subscription expired 的权益变化。
- 验证 Own AI 软件权益、订阅宽限期、退款/争议撤销；legacy credit bucket FEFO 仅作为 hosted AI 上线前的兼容能力。

暂不覆盖：

- PC/mobile 直接集成 Creem SDK 或 Creem API。
- 国内支付渠道新增开发；WeChat/Alipay 只保留 legacy 兼容。

## 依赖边界

```text
PC / Mobile
  -> walnut-billing Checkout API
    -> CheckoutService
      -> ProductRepository / UserRepository / OrderRepository
      -> CheckoutPolicy[]
        -> PaymentRiskCheckoutPolicy
          -> PaymentRiskFlagRepository
      -> PaymentService
        -> PaymentProviderAdapter(creem | mock | future overseas provider)

Provider Webhook
  -> PaymentEventHandler
    -> PaymentEventService
      -> PaymentService.VerifyWebhookEvent
        -> PaymentProviderAdapter signature + event mapping
      -> PaymentEventInboxRepository
      -> PaymentFulfillmentEventProcessor
        -> PaymentOrderEventProcessor
        -> FulfillmentService
          -> FulfillmentCatalog
          -> FulfillmentRuleExecutor(entitlement | legacy/future credits)
          -> EntitlementGrantRepository / CreditTransactionRepository
        -> PaymentAdjustmentService(refund | cancel | dispute)
          -> PaymentRiskFlagRepository
        -> SubscriptionRenewalService(renewal | grace | expired)
          -> FulfillmentService(paid renewal)
          -> EntitlementGrantRepository(subscription_grace)

PC / Mobile access gate
  -> EntitlementSnapshot / Credit snapshot only
```

边界原则：

- Provider facts 到 `PaymentEventInbox` 为止，不能直接成为 app access facts。
- `EntitlementGrant` 是 Own AI 软件访问的 Walnut 自有事实；`CreditTransaction/CreditBucket` 保留给 legacy credit SKU 与未来 hosted AI。
- 订阅宽限期只写 `EntitlementGrant(source=subscription_grace)`，不发 hosted AI credits。
- `PaymentRiskFlag` 只控制新的 checkout 尝试，不直接改写 PC/mobile 的 access snapshot。
- Admin 风险解除路径固定为 `PaymentRiskHandler -> PaymentRiskService -> PaymentRiskFlagRepository`。

## 环境准备

最低要求：

- Go toolchain 与 `go.mod` 兼容。
- SQLite 文件可写。
- `curl`；推荐安装 `jq` 以便提取响应字段。
- 如验证真实 Creem：需要 Creem API key、webhook secret、product id。

本地 mock-provider 流程推荐 `.env`：

```bash
SERVER_PORT=8082
SERVER_ENV=dev
DATABASE_DSN=./walnut_billing_local.db
DATABASE_MIGRATION_MODE=auto
ADMIN_API_KEYS=local-admin-key
CHECKOUT_RISK_POLICY_ENABLED=true
CHECKOUT_RISK_BLOCK_SEVERITIES=critical,high
# Only needed when SERVER_ENV=prod; dev can use walnut:// redirects freely.
CHECKOUT_REDIRECT_ALLOWLIST=https://app.walnut.example,walnut://
# Only needed when SERVER_ENV=prod; use exact HTTPS browser origins, no wildcard.
HTTP_CORS_ALLOWED_ORIGINS=https://app.walnut.example,https://ops.walnut.example
HTTP_SECURITY_HEADERS_ENABLED=true
HTTP_SECURITY_HEADERS_HSTS_MAX_AGE_SECONDS=31536000
```

Creem 测试流程追加配置：

```bash
PAYMENT_CREEM_API_KEY=creem_test_xxx
PAYMENT_CREEM_WEBHOOK_SECRET=whsec_xxx
PAYMENT_CREEM_SANDBOX=true
PAYMENT_CREEM_SUCCESS_URL=walnut://checkout/success
PAYMENT_CREEM_CANCEL_URL=walnut://checkout/cancel
PAYMENT_CREEM_PRODUCT_MAP_JSON='{"pro_own_ai_monthly":"prod_4MS4IC77zjEobSHExt0gcr","pro_own_ai_lifetime":"prod_lifetime_xxx"}'
```

可选的 fulfillment 覆盖配置。为空时使用内置默认规则：

```bash
FULFILLMENT_RULES_JSON='{
  "rules": [
    {
      "id": "pro_own_ai_monthly:editorial_studio",
      "sku_code": "pro_own_ai_monthly",
      "type": "grant_entitlement",
      "entitlement_id": "editorial.studio",
      "duration": "monthly"
    },
    {
      "id": "pro_own_ai_monthly:cloud_storage",
      "sku_code": "pro_own_ai_monthly",
      "type": "grant_entitlement",
      "entitlement_id": "cloud.storage",
      "duration": "monthly"
    }
  ]
}'
```

## 启动服务

```bash
cd /path/to/walnut-billing
cp .env.example .env 2>/dev/null || true
# 编辑 .env 后导出到当前 shell。
set -a
. ./.env
set +a

go run ./cmd/server
```

另开终端设置变量并检查健康状态：

```bash
BASE_URL=http://localhost:${SERVER_PORT:-8082}
ADMIN_KEY=${ADMIN_API_KEYS%%,*}
AUTH_HEADER="Authorization: Bearer ${ADMIN_KEY:-local-admin-key}"

curl -sS "$BASE_URL/ping"
curl -sS "$BASE_URL/health"
```

> 生产环境必须配置 `ADMIN_API_KEYS`。如果为空，admin routes 不会挂 API key middleware，服务会输出 warning；这只允许在隔离的本地开发环境使用。

## Mock Provider Happy Path

先用 mock provider 验证 Walnut 自有业务闭环，不调用 Creem。

### 1. 创建或识别用户

```bash
REG_RESPONSE=$(curl -sS -X POST "$BASE_URL/api/v1/registrations" \
  -H 'Content-Type: application/json' \
  -d '{
    "email": "writer@example.com",
    "display_name": "Writer",
    "requested_entitlement": "editorial.studio",
    "source": "runbook",
    "note": "local commerce flow"
  }')

echo "$REG_RESPONSE"
USER_ID=$(printf '%s' "$REG_RESPONSE" | jq -r '.user.id')
echo "$USER_ID"
```

预期：

- 响应包含 `user.id`。
- `registration.status` 为 `pending`；checkout 只要求 user active，不要求登记已审核通过。

### 2. 创建 checkout session

```bash
IDEMPOTENCY_KEY="checkout:${USER_ID}:pro_own_ai_monthly:$(date +%s)"
CHECKOUT_RESPONSE=$(curl -sS -X POST "$BASE_URL/api/v1/commerce/checkout-sessions" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"provider\": \"mock\",
    \"success_url\": \"walnut://checkout/success\",
    \"cancel_url\": \"walnut://checkout/cancel\",
    \"idempotency_key\": \"$IDEMPOTENCY_KEY\"
  }")

echo "$CHECKOUT_RESPONSE"
OUT_TRADE_NO=$(printf '%s' "$CHECKOUT_RESPONSE" | jq -r '.order.out_trade_no')
echo "$OUT_TRADE_NO"
```

预期：

- HTTP `201`。
- `order.status` 为 `checkout_created`。
- `checkout_url` 为 mock hosted checkout URL。
- 使用相同 `idempotency_key` 重试时，返回同一个 Walnut order/session 边界。

### 3. 模拟 paid webhook

推荐直接打开上一步返回的 `checkout_url`，点击本地页面的 `Simulate payment success`。这会经由 mock hosted-checkout 页面发送 paid webhook，再按 `success_url` 跳回 Walnut。

如果要绕过页面，也可以用 query/form 参数直接调用 provider-agnostic webhook inbox：

```bash
PAID_EVENT_ID="evt_paid_${OUT_TRADE_NO}"
curl -sS -X POST "$BASE_URL/api/v1/webhooks/mock?out_trade_no=$OUT_TRADE_NO&provider_event_id=$PAID_EVENT_ID&event_type=payment.paid&transaction_id=txn_$OUT_TRADE_NO&currency=USD"
```

预期：

- 响应包含 `processed: true`。
- `event.status` 为 `processed`。
- `event.event_type` 为 `payment.paid`。

### 4. 验证 order、inbox、fulfillment、snapshot

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events?out_trade_no=$OUT_TRADE_NO" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/fulfillments?out_trade_no=$OUT_TRADE_NO" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/users/$USER_ID/access?recent_limit=10" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/orders?user_id=$USER_ID&limit=10" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/subscriptions?user_id=$USER_ID&limit=10" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/cloud-storage/usage?user_id=$USER_ID&limit=10" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/users/$USER_ID/cloud-storage/projects?limit=10" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/users/$USER_ID/entitlements/snapshot"
```

预期：

- Payment event 为 `processed`，`signature_verified` 为 true。
- Fulfillment 包含 `pro_own_ai_monthly` 的当前真实高级权益 executions：editorial-studio 与 cloud-storage。
- Admin user access summary 使用同一 `SoftwareSubscriptionProjector`，可同时查看 device capacity、trial/grants、orders、payment event `payload_hash`、risk counters、cloud quota metadata；不得展示 raw email、raw device id、checkout URL、provider subscription id 或 webhook raw payload。
- Admin order list 能按 `user_id` / `out_trade_no` 定位 checkout 订单，并显示 payment event count/latest `payload_hash`、fulfillment failures、open risk count；不得展示 checkout URL、provider customer id、provider subscription id 或 idempotency key。
- Admin subscription list 能按 `user_id` / `sku_code` / `status` / `provider` / `out_trade_no` 定位月付或终身订阅，显示 Walnut subscription projection、cancel/resume flags、active fulfillment grants、latest order/provider-control status 与 payment-event `payload_hash`；不得展示 checkout URL、provider customer id、provider subscription id、provider event id、raw webhook payload 或 idempotency key。
- Admin cloud storage read model 能按 `user_id` 定位用户云用量与项目 metadata，并显示 quota、used bytes、active object count/latest manifest fingerprint；不得展示 object key、upload/download URL、local path、raw manifest hash 或文件内容。
- Snapshot 的 license state 进入 subscription，且 entitlements 包含 `editorial.studio`、`cloud.storage`。
- Signed access snapshot 的 `license.subscription_status` 来源于 `SoftwareSubscriptionProjector`；月付 active 时应为 `active` 或空，`current_period_ends_at` 与当前 Walnut period 一致。

### 4B. 验证订阅按钮状态 projection

取消月付续订后，当前周期 Pro 权益应继续可用，但重复月付 checkout 必须被阻断并提示客户端显示“恢复月付”。

```bash
CANCEL_IDEMPOTENCY_KEY="cancel:${USER_ID}:$(date +%s)"
curl -sS -X POST "$BASE_URL/api/v1/commerce/subscriptions/cancel" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"reason\": \"local projection test\",
    \"source\": \"runbook\",
    \"idempotency_key\": \"$CANCEL_IDEMPOTENCY_KEY\"
  }" | jq .

curl -i -sS -X POST "$BASE_URL/api/v1/commerce/checkout-sessions" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"provider\": \"mock\",
    \"idempotency_key\": \"checkout:${USER_ID}:monthly-after-cancel:$(date +%s)\"
  }"
```

预期：

- cancel response 的 `projection.status` 为 `cancel_at_period_end`，`projection.cancel_at_period_end=true`。
- 重新创建月付 checkout 返回 HTTP `409`，`code=checkout_blocked_by_subscription_state`，`reason=cancel_at_period_end`，`action=resume_subscription`。
- 刷新 signed access snapshot 时 `license.cancel_at_period_end=true` 且 `license.current_period_ends_at` 非空。
- 调用 `/api/v1/commerce/subscriptions/resume` 后，response 的 `projection.status=active`，重复月付 checkout 仍以 `reason=subscription_active` 阻断。
- mock paid webhook 会写入 `subscription_id=sub_mock_<out_trade_no>`；cancel/resume 因此会先走 mock `SubscriptionControlProvider`，再写 Walnut `SubscriptionCancellation` fact 和 order metadata。

恢复月付：

```bash
RESUME_IDEMPOTENCY_KEY="resume:${USER_ID}:$(date +%s)"
curl -sS -X POST "$BASE_URL/api/v1/commerce/subscriptions/resume" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"source\": \"runbook\",
    \"idempotency_key\": \"$RESUME_IDEMPOTENCY_KEY\"
  }" | jq .
```

Provider control 失败时，API 返回稳定 code：

| 场景 | HTTP | code |
|---|---:|---|
| Provider 不支持 subscription control | 409 | `subscription_control_unavailable` |
| Provider API 请求失败或返回非 2xx | 502 | `subscription_control_failed` |
| 当前用户/SKU 没有可恢复订阅 | 404 | `subscription_not_found` |

### 5. 验证重复 webhook 幂等

```bash
curl -sS -X POST "$BASE_URL/api/v1/webhooks/mock?out_trade_no=$OUT_TRADE_NO&provider_event_id=$PAID_EVENT_ID&event_type=payment.paid&transaction_id=txn_$OUT_TRADE_NO&currency=USD"
```

预期：

- 响应包含 `duplicate: true` 与 `processed: true`。
- `FulfillmentExecution` 与 `EntitlementGrant` 不重复写入；legacy credit SKU 不重复写入 `CreditTransaction`。

## Creem 测试流程

只有在 mock-provider 流程通过后，再验证真实 Creem adapter。

Creem test mode 与生产 mode 使用同一套 checkout/subscription control API，区别只在 `PAYMENT_CREEM_SANDBOX`、base URL、API key、webhook secret 与 SKU product map。Creem 官方 test mode 使用独立 test API、test product 与测试卡；Walnut adapter 不把 test/prod 差异泄露给 PC/mobile。

| 操作 | Method / Path | Test base URL |
|---|---|---|
| checkout | `POST /v1/checkouts` | `https://test-api.creem.io` |
| cancel monthly | `POST /v1/subscriptions/{id}/cancel` | `https://test-api.creem.io` |
| resume monthly | `POST /v1/subscriptions/{id}/resume` | `https://test-api.creem.io` |

Walnut 的 cancel-at-period-end 语义映射到 Creem cancel body：

```json
{"mode":"scheduled","onExecute":"cancel"}
```

后续切生产时，保持 Walnut API 不变，只把 `PAYMENT_CREEM_SANDBOX=false` 并使用生产 API key/product map/webhook secret；`PAYMENT_CREEM_API_BASE_URL` 通常继续留空，由 adapter 选择 `https://api.creem.io`。`SERVER_ENV=prod` 会在启动时强校验这些配置，test key、`https://test-api.creem.io`、缺失 product map、缺失 HTTPS CORS origins、安全响应头关闭或缺失 redirect allowlist 都会直接失败。

生产数据库必须使用 `DATABASE_MIGRATION_MODE=versioned`。启动时会先执行 `internal/app/migration` 中的有序 migration，并在 `schema_migrations` 记录 version/checksum；如果已执行 migration 的 checksum 变化，进程拒绝继续启动，避免隐式修改历史迁移。

SQLite 备份/恢复操作见 `docs/RUNBOOK_BACKUP_RESTORE.md`。放量前至少完成一次 `scripts/backup_sqlite.sh` + `scripts/verify_sqlite_restore.sh` 演练，并确认备份文件、`.sha256`、`.meta` 已复制到 off-host 存储。

### 1. 确认 Creem adapter 已注册

```bash
curl -sS "$BASE_URL/api/v1/admin/payment/providers" \
  -H "$AUTH_HEADER"
```

预期：

- `creem` 出现在 provider 列表中。
- `is_mock` 为 false。
- `sandbox_mode` 与 `PAYMENT_CREEM_SANDBOX` 一致。
- Provider 状态 active 后，checkout、webhook、subscription cancel/resume 都走同一个 Creem adapter 边界；App 不接触 Creem subscription id。

如果未出现 `creem`，检查：

- `PAYMENT_CREEM_API_KEY`
- `PAYMENT_CREEM_WEBHOOK_SECRET`
- `PAYMENT_CREEM_PRODUCT_MAP_JSON`
- 启动日志中的 `Creem checkout adapter not initialized`

如需运行时热更新 Creem 配置，可调用：

```bash
curl -sS -X PUT "$BASE_URL/api/v1/admin/payment/creem" \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{
    "api_key": "creem_test_xxx",
    "webhook_secret": "whsec_xxx",
    "success_url": "walnut://checkout/success",
    "cancel_url": "walnut://checkout/cancel",
    "sandbox": true,
    "product_ids": {
      "pro_own_ai_monthly": "prod_4MS4IC77zjEobSHExt0gcr",
      "pro_own_ai_lifetime": "prod_lifetime_xxx"
    }
  }'
```

### 2. 创建 Creem checkout session

```bash
IDEMPOTENCY_KEY="checkout:${USER_ID}:pro_own_ai_monthly:creem:$(date +%s)"
CHECKOUT_RESPONSE=$(curl -sS -X POST "$BASE_URL/api/v1/commerce/checkout-sessions" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"provider\": \"creem\",
    \"success_url\": \"walnut://checkout/success\",
    \"cancel_url\": \"walnut://checkout/cancel\",
    \"idempotency_key\": \"$IDEMPOTENCY_KEY\"
  }")

echo "$CHECKOUT_RESPONSE"
OUT_TRADE_NO=$(printf '%s' "$CHECKOUT_RESPONSE" | jq -r '.order.out_trade_no')
```

预期：

- HTTP `201`。
- `checkout_url` 指向 Creem hosted checkout。
- `provider_checkout_id` 已填充。

### 3. 接收真实 Creem webhook

在 Creem dashboard 中将 webhook URL 配置为：

```text
https://<public-host>/api/v1/webhooks/creem
```

本地测试可通过 tunnel 或反向代理暴露服务，再把 Creem webhook endpoint 指向 tunnel URL。PC/mobile 仍只调用 walnut-billing，不调用 Creem。

成功月付 checkout/renewal webhook 必须包含或可从 payload 推导 Creem subscription id；billing 会把它记录为 order metadata 中的 `walnut_provider_subscription_id`。后续 `/commerce/subscriptions/cancel` 和 `/commerce/subscriptions/resume` 通过该 id 调用 Creem subscription control，再写 Walnut 自有 projection。若 provider 调用失败，Walnut 不写取消/恢复 fact，用户可用相同 `idempotency_key` 重试。

### 4. 本地模拟 Creem 签名 webhook

用于不依赖 Creem dashboard 的确定性验证。

```bash
PAYLOAD=$(cat <<JSON
{"id":"evt_local_paid_1","eventType":"checkout.completed","object":{"id":"ch_1","request_id":"$OUT_TRADE_NO","order":{"id":"ord_1","amount":500,"currency":"USD","status":"paid"},"metadata":{"walnut_out_trade_no":"$OUT_TRADE_NO"}}}
JSON
)

SIG=$(printf '%s' "$PAYLOAD" | openssl dgst -sha256 -hmac "$PAYMENT_CREEM_WEBHOOK_SECRET" -binary | xxd -p -c 256)

curl -sS -X POST "$BASE_URL/api/v1/webhooks/creem" \
  -H 'Content-Type: application/json' \
  -H "creem-signature: $SIG" \
  -d "$PAYLOAD"
```

预期：

- Creem adapter 将 `checkout.completed` 映射为 `payment.paid`。
- 金额和币种必须匹配 Walnut order；不匹配会失败并进入可排障/重试路径。
- 后续验证与 mock happy path 一致。

## Credit Bucket / Expiry

当前 Own AI SKU 不包含 hosted AI credits；这里保留 legacy credit bucket 验收，确保未来 hosted AI 上线时仍能按 bucket 维度区分订阅赠点和预存点数。客户端仍然只看聚合余额与 entitlement snapshot，bucket 只在 billing 内部用于过期、退款和可追踪性。

### 1. Own AI 订阅权益

`pro_own_ai_monthly` fulfillment 会写入：

- `EntitlementGrant(source=fulfillment)`
- `EntitlementID` 覆盖当前真实高级权益：`editorial.studio`、`cloud.storage`
- `ExpiresAt = paid period end`

### 2. Legacy credit bucket

当前 Own AI 商业化不出售 hosted AI credits。`credits_600` 是历史测试 SKU，会写入：

- `CreditBucket(type=topup)`
- `ExpiresAt = nil`
- 不随订阅周期自动过期

### 3. Legacy credit bucket 运行期过期

可以由 operator 或定时任务调用：

```bash
curl -sS -X POST "$BASE_URL/api/v1/admin/credits/buckets/expire" \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{"limit":100}'
```

预期：

- 只过期 `ExpiresAt <= now` 且 `Remaining > 0` 的 bucket。
- ledger transaction type 记为 `expire`；admin audit action 记为 `credit.expire`。
- 只扣减原始 bucket 对应余额，不影响其他未来 top-up。

### 4. FEFO 消耗

当用户使用 legacy 或未来 hosted AI credits 时，系统按最早到期优先（FEFO）消费。订阅周期 credits 会优先被消费，top-up credits 作为后备余额保留。

### 5. 退款/clawback

退款或 dispute 只回收原始发放 bucket 中仍然可用的余额，不会跨 bucket 回收未来充值。

## Subscription Renewal / Grace Period

该流程验证 provider subscription 事件不会直接驱动 PC/mobile 门禁，而是通过 Walnut 的 `Order` 和 `EntitlementGrant` 生效；未来 hosted AI credits 再由 `CreditTransaction` 补充。Creem 官方事件映射：

| Creem event | Walnut event | 行为 |
|---|---|---|
| `subscription.paid` | `payment.renewal_paid` | 续费成功，执行新周期 fulfillment，发放 Own AI entitlement |
| `subscription.past_due` | `payment.renewal_failed` | 进入 grace period，仅保留高级权益，不发 hosted AI credits |
| `subscription.expired` | `payment.subscription_expired` | 触发 grace 到期检查；若仍在 grace 窗口内则不截断，若已到 `expires_at` 后默认标记 `subscription_grace` expired |

本地模拟续费失败：

```bash
PAST_DUE_PAYLOAD=$(cat <<JSON
{"id":"evt_sub_past_due_1","eventType":"subscription.past_due","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"$OUT_TRADE_NO"}},"order":{"id":"ord_renewal_failed_1","amount":500,"currency":"USD"},"current_period_start_date":"2026-07-12T10:00:00.000Z","current_period_end_date":"2026-08-12T10:00:00.000Z"}}
JSON
)
SIG=$(printf '%s' "$PAST_DUE_PAYLOAD" | openssl dgst -sha256 -hmac "$PAYMENT_CREEM_WEBHOOK_SECRET" -binary | xxd -p -c 256)
curl -sS -X POST "$BASE_URL/api/v1/webhooks/creem" \
  -H 'Content-Type: application/json' \
  -H "creem-signature: $SIG" \
  -d "$PAST_DUE_PAYLOAD"
```

预期：

- `PaymentEventInbox.event_type=payment.renewal_failed`。
- 若 webhook 只携带原 checkout `out_trade_no`，billing 会按 `source_out_trade_no + billing period` 派生 Walnut renewal order，避免 provider 订单直接进入门禁。
- 创建 `EntitlementGrant(source=subscription_grace)`，`starts_at = current_period_end_date`，`expires_at = current_period_end_date + RENEWAL_GRACE_PERIOD_DAYS`。
- 不新增 hosted AI credit grant transaction。

本地模拟续费成功：

```bash
PAID_PAYLOAD=$(cat <<JSON
{"id":"evt_sub_paid_1","eventType":"subscription.paid","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"$OUT_TRADE_NO"}},"order":{"id":"ord_renewal_paid_1","amount":500,"currency":"USD"},"current_period_start_date":"2026-07-12T10:00:00.000Z","current_period_end_date":"2026-08-12T10:00:00.000Z"}}
JSON
)
SIG=$(printf '%s' "$PAID_PAYLOAD" | openssl dgst -sha256 -hmac "$PAYMENT_CREEM_WEBHOOK_SECRET" -binary | xxd -p -c 256)
curl -sS -X POST "$BASE_URL/api/v1/webhooks/creem" \
  -H 'Content-Type: application/json' \
  -H "creem-signature: $SIG" \
  -d "$PAID_PAYLOAD"
```

预期：

- `PaymentEventInbox.event_type=payment.renewal_paid`。
- 已付续费 order 经 `FulfillmentService` 执行新周期 entitlement；重复 webhook 不重复发放。
- 首次订阅付款若同时收到 `checkout.completed` 和 `subscription.paid`，使用 checkout fulfillment 幂等键，不重复发放。

本地模拟 grace 结束：

> 注意：Creem 的 `subscription.expired` 可能在 paid period 结束时到达；Walnut 的 grace 从 `current_period_end_date` 开始计算。若在 grace 窗口内收到该事件，事件会 processed，但 access 继续有效直到 `subscription_grace.expires_at`。要验证主动过期，请把 `current_period_end_date` 设置为当前时间至少 `RENEWAL_GRACE_PERIOD_DAYS` 天以前，或等 grace 自然结束后 reprocess。

```bash
EXPIRED_PAYLOAD=$(cat <<JSON
{"id":"evt_sub_expired_1","eventType":"subscription.expired","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"$OUT_TRADE_NO"}},"current_period_start_date":"2026-07-12T10:00:00.000Z","current_period_end_date":"2026-08-12T10:00:00.000Z"}}
JSON
)
SIG=$(printf '%s' "$EXPIRED_PAYLOAD" | openssl dgst -sha256 -hmac "$PAYMENT_CREEM_WEBHOOK_SECRET" -binary | xxd -p -c 256)
curl -sS -X POST "$BASE_URL/api/v1/webhooks/creem" \
  -H 'Content-Type: application/json' \
  -H "creem-signature: $SIG" \
  -d "$EXPIRED_PAYLOAD"
```

预期：

- 默认 `RENEWAL_EXPIRED_ACTION=expire_grace` 只会把已到 `expires_at` 的相关 `subscription_grace` grant 标记为 expired；早到的 `subscription.expired` 不截断 grace。
- 若配置 `RENEWAL_EXPIRED_ACTION=natural_expiry`，不主动改写 grant，等待 `expires_at` 自然失效。
- PC/mobile 仍只通过 snapshot 看最终 entitlement 状态。

## Dispute / Chargeback 流程

该流程验证 refund/dispute 能安全撤销履约、回收 legacy/future credits，并产生人工审核 checkout hold。

### 1. 用 mock provider 模拟 dispute

```bash
DISPUTE_EVENT_ID="evt_dispute_${OUT_TRADE_NO}"
curl -sS -X POST "$BASE_URL/api/v1/webhooks/mock?out_trade_no=$OUT_TRADE_NO&provider_event_id=$DISPUTE_EVENT_ID&event_type=payment.disputed&transaction_id=disp_$OUT_TRADE_NO&currency=USD"
```

预期：

- 响应包含 `processed: true`。
- Order status 变为 `refunded`。
- 本订单产生的 entitlement grant 被 revoke。
- 若本订单发放过 legacy/future credits，则按可用余额 clawback，不产生负余额。
- 创建一条 open critical `PaymentRiskFlag`。

### 2. 用 Creem payload 模拟 dispute

```bash
PAYLOAD=$(cat <<JSON
{"id":"evt_local_dispute_1","eventType":"dispute.created","object":{"id":"disp_1","dispute":{"id":"disp_1","amount":500,"currency":"USD","metadata":{"walnut_out_trade_no":"$OUT_TRADE_NO"}}}}
JSON
)

SIG=$(printf '%s' "$PAYLOAD" | openssl dgst -sha256 -hmac "$PAYMENT_CREEM_WEBHOOK_SECRET" -binary | xxd -p -c 256)

curl -sS -X POST "$BASE_URL/api/v1/webhooks/creem" \
  -H 'Content-Type: application/json' \
  -H "creem-signature: $SIG" \
  -d "$PAYLOAD"
```

预期：

- Creem adapter 将 `dispute.created` 映射为 `payment.disputed`。
- `PaymentAdjustmentService` 执行 revoke/clawback，并创建风险标记。

### 3. 验证 snapshot 和 risk flag

```bash
curl -sS "$BASE_URL/api/v1/users/$USER_ID/entitlements/snapshot"

RISK_RESPONSE=$(curl -sS "$BASE_URL/api/v1/admin/payment-risk-flags?user_id=$USER_ID&status=open" \
  -H "$AUTH_HEADER")
echo "$RISK_RESPONSE"
RISK_ID=$(printf '%s' "$RISK_RESPONSE" | jq -r '.risk_flags[0].id')
echo "$RISK_ID"
```

预期：

- Snapshot 不再包含本订单对应的 active `editorial.studio`。
- 若该订单包含 credits，`credits.balance` 回到 clawback 后的预期余额；Own AI SKU 可保持 0。
- Risk flag 为 `status=open`、`severity=critical`、`reason=dispute`。

### 4. 验证 checkout hold

```bash
BLOCKED_IDEMPOTENCY_KEY="checkout:${USER_ID}:blocked:$(date +%s)"
curl -i -sS -X POST "$BASE_URL/api/v1/commerce/checkout-sessions" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"provider\": \"mock\",
    \"idempotency_key\": \"$BLOCKED_IDEMPOTENCY_KEY\"
  }"
```

预期：

- HTTP `403`。
- 响应包含 `code=checkout_blocked_by_payment_risk` 与 `action=manual_review`。
- 阻断发生在 provider 调用前，不应创建新的 provider checkout。

## 人工审核与风险解除

运营确认用户可恢复购买后，通过 admin resolve API 解除风险 hold。

```bash
curl -sS -X POST "$BASE_URL/api/v1/admin/payment-risk-flags/$RISK_ID/resolve" \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{
    "resolved_by": "ops",
    "note": "verified customer; allow checkout again"
  }'
```

预期：

- Risk flag `status` 变为 `resolved`。
- `resolved_by`、`resolved_at` 和 resolution note 已填充。
- 写入审计 action `payment_risk.resolve`。
- 同一用户可重新创建 checkout。

```bash
POST_RESOLVE_IDEMPOTENCY_KEY="checkout:${USER_ID}:post-resolve:$(date +%s)"
curl -sS -X POST "$BASE_URL/api/v1/commerce/checkout-sessions" \
  -H 'Content-Type: application/json' \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"sku_code\": \"pro_own_ai_monthly\",
    \"provider\": \"mock\",
    \"idempotency_key\": \"$POST_RESOLVE_IDEMPOTENCY_KEY\"
  }"
```

## Admin Reprocess 流程

当 webhook 已进入 inbox，但处理失败或被退款策略转入人工处理，可通过 admin reprocess 修复可恢复故障。

完整 webhook retry / dead-letter / admin reprocess 操作手册见 `docs/RUNBOOK_WEBHOOK_OPERATIONS.md`；本节只保留本地验证命令。

```bash
curl -sS "$BASE_URL/api/v1/admin/payment-events?status=failed" \
  -H "$AUTH_HEADER"

curl -sS "$BASE_URL/api/v1/admin/payment-events?status=review_required" \
  -H "$AUTH_HEADER"

curl -sS -X POST "$BASE_URL/api/v1/admin/payment-events/<payment_event_id>/reprocess" \
  -H "$AUTH_HEADER"
```

通常可重试：

- 履约依赖短暂失败。
- 未来多数据库拓扑下，订单短暂不可见。
- 退款策略或低使用阈值调整后，原 `review_required` 事件需要继续执行补偿。

通常不能直接重试，需要先调查：

- 签名无效：事件会在入 inbox 前被拒绝。
- 金额或币种不匹配：必须先确认 provider/product/order 映射。
- 未知 `out_trade_no`：无法映射到 Walnut order。
- `policy_rejected`：这是策略终态，除非业务确认并调整 `ADJUSTMENT_*` 策略，否则不要反复 reprocess。
- 多次 reprocess 仍失败：按 webhook operations runbook 留在 inbox 作为 dead-letter 运营队列，不要直接 SQL 改状态。

策略说明：

- `review_required` / `policy_rejected` 是 Walnut policy decision，不是 provider 处理失败；webhook 响应会保持 accepted，避免 Creem 因业务人工审核反复重投。
- `PaymentAdjustmentPolicy` 只根据 Walnut 订单、履约记录和 credits ledger 做决策；Creem adapter 只归一化支付事实，不承载退款业务规则。


## 可观测性与告警

生产环境至少需要采集 `/metrics` 与结构化日志。商业化链路通过 service decorator 统一观测，provider adapter、handler 和 PC/mobile 不直接写业务指标，避免 Creem 或未来渠道反向污染 Walnut 门禁模型。

关键日志事件：

| 事件 | 关键字段 | 用途 |
|---|---|---|
| `commerce_checkout_observed` | `provider`、`sku_code`、`user_id`、`out_trade_no`、`status`、`error_kind`、`policy_reason`、`policy_action` | 定位 checkout 创建失败、provider timeout、risk hold |
| `payment_event_observed` | `operation`、`provider`、`provider_event_id`、`event_type`、`out_trade_no`、`inbox_status`、`attempts`、`error_kind` | 定位 webhook 验签、幂等、处理失败与 reprocess |
| `commerce_fulfillment_observed` | `out_trade_no`、`user_id`、`sku_code`、`order_type`、`status`、`execution_count`、`error_kind` | 定位 paid 后未履约、重复履约保护 |
| `payment_adjustment_observed` | `provider`、`provider_event_id`、`event_type`、`out_trade_no`、`status`、`policy_action`、`policy_reason`、`risk_flag_created` | 定位 refund/dispute/cancel 策略和风险标记 |

关键 metrics：

| Metric | 关注点 | 建议告警 |
|---|---|---|
| `payment_events_total{status="failed"}` | webhook 处理失败 | 5 分钟内持续增长时排查 provider payload、order 映射或履约依赖 |
| `payment_events_total{error_kind="signature_verification_failed"}` | webhook 验签失败 | 任意突增都检查 Creem webhook secret、代理是否改写 raw body |
| `payment_events_total{error_kind="amount_mismatch"}` / `currency_mismatch` | 金额/币种不匹配 | 按高风险处理，不手工强制 paid；核对 SKU/product map |
| `fulfillments_total{status="failed"}` | 履约失败 | 修复 rule/repository 后通过 admin reprocess 恢复 |
| `checkout_policy_blocks_total` | risk hold 数量 | 突增时检查 dispute 来源和误伤，必要时运营 review/resolve |
| `payment_adjustments_total{status="review_required"}` | 退款人工审核积压 | 排队处理，确认后调整 policy 或 reprocess |
| `subscription_actions_total{status="failed"}` | 退订/恢复 provider control 失败 | 不写本地取消/恢复事实；修复 provider 后用相同 idempotency key 重试 |
| `access_snapshots_total{error_kind="signature_error"}` | signed snapshot 签名失败 | 立即检查 prod signer/key id，必要时回滚 signer 配置 |
| `cloud_sync_total{error_kind="over_quota"}` | 云同步超配额 | 通过 admin cloud metadata 判断清理/升级路径，不查看对象正文 |
| `admin_actions_total{success="false"}` | 管理端写操作失败 | 检查权限、审计和操作原因，不重复盲试 |

日志安全约束：

- 不记录 raw webhook payload、完整 headers、API key、webhook secret、checkout URL token。
- 指标 label 只使用低基数字段：provider、event_type、status、sku_code、order_type、policy_action、error_kind；`user_id`、`out_trade_no`、`provider_event_id` 只进入日志。
- 签名失败事件可能不会入 inbox，但会产生 `payment_event_observed`，用于定位 provider secret / proxy 问题。
- 更完整的 secret redaction、raw payload retention、PII retention 与 admin action review 见 `docs/RUNBOOK_SECURITY_AUDIT.md`。
- 生产监控面板、告警阈值和 owner 分流见 `docs/RUNBOOK_MONITORING_ALERTS.md`。
- 云存储 sync session、manifest commit 和 restore metadata 见 `docs/RUNBOOK_CLOUD_STORAGE_CONTROL_PLANE.md`。

## 排障矩阵

| 现象 | 可能原因 | 检查 | 处理 |
|---|---|---|---|
| Creem provider `disabled` | 未配置 Creem，当前只跑 mock profile | `/admin/payment/providers` | 如需 Creem test mode，设置 test API key、webhook secret 和完整 product map |
| Creem provider `error` | 缺少 product map、test/prod endpoint/key 混用、webhook secret 缺失 | `/admin/payment/providers` 的 `error` 字段；启动日志 | 修正 `PAYMENT_CREEM_*`，确保 sandbox 使用 `https://test-api.creem.io` 与 test key |
| 启动失败：`DATABASE_MIGRATION_MODE must be versioned in prod` | 生产仍使用 dev `auto` 迁移策略 | `.env` / secret manager / 启动日志 | 设置 `DATABASE_MIGRATION_MODE=versioned`，并先在备份副本验证 migration |
| 需要回滚/恢复 SQLite DB | 部署失败、DB 损坏或误操作 | `docs/RUNBOOK_BACKUP_RESTORE.md`、`.sha256`、`schema_migrations` | 停服务，保存当前 DB/WAL/SHM，复制已验证 backup，删除旧 WAL/SHM，启动后检查 `/health` 与 admin read models |
| 浏览器请求被 CORS 拦截 | Origin 不在 `HTTP_CORS_ALLOWED_ORIGINS` 或生产配置使用了 wildcard/http origin | 浏览器 devtools、响应头、启动日志中的 `ErrInvalidProductionConfig` | 将 App/Admin Web 的精确 HTTPS origin 加入 allowlist；不要使用 `*` 或带 path/query 的 URL |
| checkout 返回 `payment provider not found: creem` | Creem 当前不是 active provider | `/admin/payment/providers` | 先消除 `disabled/error` 状态，再重试 checkout |
| checkout 返回 `checkout_provider_failed` | provider 请求失败或 SKU 未映射 | 服务日志；响应 body | 检查 product map、API base URL、网络、Creem credentials |
| checkout 返回 `checkout_redirect_not_allowed` | success/cancel URL 不在生产 allowlist | `CHECKOUT_REDIRECT_ALLOWLIST`；响应 `reason=redirect_not_allowed` | 将 App/Web 回跳 origin 或 app scheme 加入 allowlist 后重试 |
| checkout 返回 `checkout_blocked_by_payment_risk` | 存在 open high/critical `PaymentRiskFlag` | `/admin/payment-risk-flags?user_id=...&status=open` | 仅在人工审核后 resolve |
| checkout 返回 `checkout_blocked_by_subscription_state` | `SoftwareSubscriptionProjector` 判断已有 active/cancel-at-period-end/lifetime access | response `reason`/`action`；signed snapshot license | 按 `already_lifetime`、`subscription_active`、`cancel_at_period_end` 展示保留权益、管理订阅或恢复月付 |
| webhook 返回 bad request | 签名或 payload 无效 | `creem-signature`、raw payload、secret | 重算签名；核对 dashboard secret |
| event 因 amount mismatch failed | Provider amount 与 Walnut order 不一致 | event `amount`；order `amount` | 按高风险处理，不能手工强行 paid |
| event 因 currency mismatch failed | Provider currency 与 order 不一致 | event `currency`；order `currency` | 修正 product/provider mapping 后再 reprocess |
| duplicate webhook 未重复发放权益 | 这是预期幂等行为 | `provider_event_id`；fulfillment executions | 无需处理 |
| fulfillment 缺失 | paid event failed 或 fulfillment rule 缺失 | `/admin/payment-events`、`/admin/fulfillments` | 修复规则/配置后 reprocess |
| snapshot 未出现 entitlement | grant 缺失、过期或已 revoke | `/admin/grants`、snapshot | 检查 fulfillment execution 与 grant status |
| dispute 后未阻断 checkout | 风险策略关闭或 severity 配置未包含该 flag | `CHECKOUT_RISK_POLICY_ENABLED`、`CHECKOUT_RISK_BLOCK_SEVERITIES` | 开启策略或修正 severity 配置 |

## 生产前检查清单

真实海外 checkout 放量前必须确认：

- [ ] `ADMIN_PRINCIPALS_JSON` 或 `ADMIN_API_KEYS` 非空；生产优先 scoped principals。
- [ ] `DATABASE_MIGRATION_MODE=versioned`，目标 DB 备份已完成，`schema_migrations` 记录可审计。
- [ ] `scripts/verify_sqlite_backup_contract.sh` 通过，最新生产备份已完成 restore drill 并复制到 off-host 存储。
- [ ] `PAYMENT_CREEM_API_KEY` 与 `PAYMENT_CREEM_WEBHOOK_SECRET` 存在 secret manager，不进入日志。
- [ ] `PAYMENT_CREEM_SANDBOX=false`，且未使用 test key 或 `https://test-api.creem.io`。
- [ ] `PAYMENT_CREEM_PRODUCT_MAP_JSON` 覆盖所有可见海外 SKU。
- [ ] `FULFILLMENT_RULES_JSON` 已评审，或明确接受内置默认规则。
- [ ] `CHECKOUT_RISK_POLICY_ENABLED=true`，且 `CHECKOUT_REDIRECT_ALLOWLIST` 覆盖 App/Web 回跳 origin 或 app scheme。
- [ ] `HTTP_CORS_ALLOWED_ORIGINS` 覆盖 App/Admin Web 精确 HTTPS origins，`HTTP_SECURITY_HEADERS_ENABLED=true`，HSTS 至少一年。
- [ ] `ADJUSTMENT_REFUND_WINDOW_DAYS`、`ADJUSTMENT_*_ACTION` 和低使用阈值已按业务策略评审。
- [ ] `RENEWAL_GRACE_PERIOD_DAYS` 与 `RENEWAL_EXPIRED_ACTION` 已按业务策略评审。
- [ ] 公网 webhook endpoint 使用 TLS，并保持 raw request body 不被代理改写。
- [ ] Creem dashboard webhook URL 指向 `/api/v1/webhooks/creem`。
- [ ] 目标环境 happy path 与 dispute path 均通过。
- [ ] `scripts/verify_production_config_contract.sh` 通过。
- [ ] 部署 commit 通过 `go test ./...`。
- [ ] 运营知道如何 list failed events、reprocess events、list risk flags、resolve risk flags。
- [ ] `docs/RUNBOOK_WEBHOOK_OPERATIONS.md` 已演练，`failed/review_required/policy_rejected` 的 owner 和升级路径明确。
- [ ] `docs/RUNBOOK_SECURITY_AUDIT.md` 已演练，确认 config update audit 不含 secret，admin read models 不暴露 raw payload/provider IDs。
- [ ] `docs/RUNBOOK_MONITORING_ALERTS.md` 已演练，确认 checkout/webhook/fulfillment/snapshot/cloud/subscription/admin 告警 owner 明确。

## 当前质量门禁

关闭 P0 runbook 前执行：

```bash
scripts/verify_production_config_contract.sh
scripts/verify_database_migration_contract.sh
scripts/verify_sqlite_backup_contract.sh
scripts/verify_webhook_operations_contract.sh
scripts/verify_security_audit_contract.sh
scripts/verify_monitoring_contract.sh
scripts/verify_cloud_storage_control_contract.sh
go test ./...
git diff --check
rg -n "creem|Creem|PaymentRiskFlag|payment\\.disputed|checkout_blocked_by_payment_risk|PaymentRiskCheckoutPolicy" ../sagemate-core ../walnut-mobile --glob '!**/.git/**' --glob '!**/docs/**' || true
```

预期：

- 全量测试通过。
- 无 whitespace error。
- PC/mobile 非 docs 代码没有直接依赖 Creem、payment risk 内部模型或 checkout risk 实现名。
