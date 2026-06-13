# Issue: Walnut 商品支付与权益履约模型 - Creem 集成准备

## 背景

Issue #44 已经把 Walnut 的权益门禁、用户登记、人工 grant、Credits 钱包、使用预扣和 PC Core facade 逐步收敛到稳定边界：

```text
App / PC Core
  -> AccessDecision / AccessSnapshot
    -> walnut-billing EntitlementGrant + CreditTransaction
```

后续接入 Creem 支付时，不能把支付平台状态直接暴露给前端、PC Core 或编辑部业务模块。Creem 应作为 `walnut-billing` 内部的支付渠道适配器，支付成功后通过 Walnut 自己的履约规则生成：

- `EntitlementGrant`：如 `editorial.studio`。
- `CreditTransaction`：如点数包、订阅赠点、活动赠点。
- 后续团队额度池或 seats 的内部记录。

## 产品边界

- **基础编辑部 / 稿件库 / 人工编辑稿件**：基础功能，不因支付系统接入而加门禁。
- **编辑部工作室 / 召唤编辑部 / 多 Agent 协作**：高级功能，最终由 `editorial.studio` entitlement 和 credits 共同控制。
- **VIP**：仅是展示文案，不作为授权判断字段。
- **Creem**：只负责 checkout、支付状态、订阅事件、税务/MoR 合规能力；不作为 Walnut 的门禁 source of truth。
- **海外渠道优先**：当前商业化支付只推进海外 hosted checkout 渠道；WeChat/Alipay 等国内支付仅保留历史兼容，不再作为 M6 新开发目标。

## 当前架构评估

`walnut-billing` 已经具备部分可复用基础：

| 现有能力 | 当前职责 | M6 复用方式 |
|---|---|---|
| `Product` | 旧 license 商品 | 演进为商品目录的兼容层，后续拆出 `SKU` / `FulfillmentRule` |
| `Order` | 支付订单，当前强绑定 `LicenseKey` | 保留旧路径，新增用户维度和 SKU/checkout 维度 |
| `PaymentProvider` | legacy WeChat / Alipay 适配器接口 | 升级为海外 hosted checkout + webhook adapter，当前优先 Creem |
| `ProviderRegistry` | 支付渠道注册表 | 继续作为 Strategy / Registry 扩展点 |
| `PaymentService` | 创建支付 URL、处理回调、激活 license | 拆分为 `CheckoutService`、`WebhookInboxService`、`FulfillmentService` |
| `EntitlementService` | 登记、人工 grant、snapshot | 作为履约落地目标，不关心支付渠道 |
| `CreditService` | credits grant / reserve / commit / release | 作为点数履约目标，不关心支付渠道 |

需要避免的架构冲突：

1. `Order.LicenseKey` 与支付成功后激活 license 的逻辑耦合较重，不能直接承载点数包和权益组合包。
2. `PaymentProvider.VerifyCallback(params map[string]string)` 偏传统表单回调，不足以表达 Creem 的 JSON webhook、event id、签名头和订阅事件。
3. `PaymentService.HandleCallback()` 当前在回调处理中直接更新订单并激活 license，缺少 webhook inbox、幂等事件处理和独立履约层。
4. `Product` 目前只有价格和有效期，不足以表达“一个 SKU 同时发放 entitlement + credits + 周期性 allowance”。
5. 当前订单缺少 `user_id`、`sku_code`、`provider_checkout_id`、`provider_customer_id` 等商业化字段，需要渐进式扩展，不能破坏旧 license 购买路径。

## 目标架构

```text
PC / Mobile / Frontend
  -> PC Core access facade
    -> walnut-billing CheckoutFacade
      -> CommerceCatalog
        -> Product / SKU / FulfillmentRule
      -> Order
      -> PaymentProviderAdapter(creem | overseas_next | mock)
        -> checkout_url

Creem Webhook
  -> PaymentWebhookHandler
    -> PaymentEventInbox(signature + idempotency)
      -> PaymentEventProcessor
        -> Order state transition
        -> FulfillmentService
          -> EntitlementService.CreateGrant
          -> CreditService.Grant
          -> TeamQuotaService later
        -> EntitlementSnapshot consumed by PC Core
```

核心原则：

- App 只发起 checkout 或刷新 snapshot，不直接读取 Creem。
- Billing 内部以 `Order` 和 `PaymentEventInbox` 管理支付事实。
- Walnut 权益以 `EntitlementGrant` 和 `CreditTransaction` 为最终事实。
- Creem subscription 状态只驱动履约，不直接等于 access allowed。

## 核心领域模型

### Product

面向产品展示的分组，例如：

- `walnut_editorial_studio`
- `walnut_credits`
- `walnut_team`

Product 不直接决定权益，只聚合 SKU。

### SKU

真正可售卖的库存单位，包含价格、币种、周期、可见性和 provider 映射：

```text
sku_code: editorial_studio_monthly
product_code: walnut_editorial_studio
billing_period: monthly | yearly | one_time
price_cents: 1900
currency: USD
provider_refs:
  creem_product_id: prod_xxx
  creem_price_id: price_xxx
```

### FulfillmentRule

SKU 支付成功后执行的履约规则。规则应配置化，避免把商品权益硬编码到 webhook 中：

```json
{
  "sku_code": "editorial_studio_monthly",
  "rules": [
    {
      "type": "grant_entitlement",
      "entitlement_id": "editorial.studio",
      "duration": "subscription_period"
    },
    {
      "type": "grant_credits",
      "amount": 600,
      "source": "subscription_allowance"
    }
  ]
}
```

### Order

Walnut 内部订单事实，不等同于 Creem checkout。建议新增或扩展字段：

```text
id / out_trade_no
user_id
sku_code
amount / currency
status: pending | checkout_created | paid | fulfilled | cancelled | refunded | failed
provider
provider_checkout_id
provider_order_id
provider_customer_id
idempotency_key
metadata
created_at / paid_at / fulfilled_at
```

旧 license 订单继续保留 `LicenseKey` 路径，新商品化订单通过 `sku_code + user_id + fulfillment` 路径履约。

### PaymentEventInbox

所有支付 webhook 先落 inbox，再异步或同步处理：

```text
provider: creem
provider_event_id
event_type
signature_verified
payload_hash
raw_payload
status: received | processing | processed | ignored | failed
attempts
last_error
received_at / processed_at
```

唯一键建议为 `(provider, provider_event_id)`，保证重复 webhook 安全。

### FulfillmentExecution

履约结果记录，防止同一订单重复发放权益或点数：

```text
order_id
sku_code
rule_id
target_type: entitlement | credits | team_quota | license
idempotency_key
status: applied | skipped | failed
result_ref
created_at
```

## 服务边界与设计模式

| 服务 / 抽象 | 模式 | 职责 |
|---|---|---|
| `CommerceCatalog` | Catalog / Policy | 解析 Product、SKU、FulfillmentRule，校验稳定 entitlement id |
| `CheckoutFacade` | Facade | 给 PC Core / 移动端提供统一 checkout 入口 |
| `PaymentProviderAdapter` | Strategy / Adapter | 屏蔽 Creem 与未来海外渠道差异；国内支付不再扩展 |
| `PaymentProviderRegistry` | Registry | 按 provider name 获取 adapter |
| `PaymentEventInboxService` | Inbox / Idempotency | 保存、去重、重试 webhook 事件 |
| `PaymentEventProcessor` | Application Service | 把 provider event 转换为 Walnut order 状态变化 |
| `FulfillmentService` | Rule Engine / UnitOfWork | 根据 FulfillmentRule 原子化发放权益和 credits |
| `EntitlementService` | Facade | 负责 grant 和 snapshot 投影 |
| `CreditService` | Ledger Facade | 负责 credits 账本，不感知 Creem |

## API 草案

### Client / PC Core

```http
POST /api/v1/commerce/checkout-sessions
```

请求：

```json
{
  "user_id": "usr_xxx",
  "sku_code": "editorial_studio_monthly",
  "provider": "creem",
  "success_url": "walnut://checkout/success",
  "cancel_url": "walnut://checkout/cancel",
  "idempotency_key": "checkout:usr_xxx:editorial_studio_monthly:..."
}
```

响应：

```json
{
  "order_id": "ord_xxx",
  "out_trade_no": "ORD-...",
  "provider": "creem",
  "checkout_url": "https://...",
  "status": "checkout_created"
}
```

### Webhook

```http
POST /api/v1/webhooks/creem
```

要求：

- 校验 Creem 签名。
- 保存原始 payload 或安全裁剪 payload。
- 通过 `provider_event_id` 幂等。
- 不在 handler 中堆业务分支，交给 `PaymentEventProcessor`。

### Admin

```http
GET  /api/v1/admin/commerce/orders?user_id=&status=&provider=
GET  /api/v1/admin/payment-events?provider=&status=&type=
POST /api/v1/admin/payment-events/:id/reprocess
GET  /api/v1/admin/fulfillments?order_id=&user_id=
```

## 可配置项

| 配置 | 建议来源 | 说明 |
|---|---|---|
| SKU catalog | 数据库或 JSON seed | 商品、价格、周期、provider ref |
| Fulfillment rules | 数据库或版本化 JSON | SKU 到 entitlement / credits / team quota 的映射 |
| Provider credentials | 环境变量 / secret manager | Creem API key、webhook secret |
| Checkout URLs | 环境变量 + request override | success/cancel URL 白名单 |
| Entitlement duration policy | FulfillmentRule | one-time、固定天数、subscription period |
| Credit grant amount | FulfillmentRule | 点数包和订阅赠点均配置化 |
| Refund policy | Policy config | 退款后 revoke grant、扣回未用 credits 或仅停止续费 |
| Checkout risk policy | Policy config / DB | open high/critical 风险标记是否阻断 checkout、转人工审核 action |
| Retry policy | config | webhook 处理失败后的最大重试次数和退避策略 |

## 推荐里程碑

### M6-A: Commerce Catalog 与履约规则文档化

范围：

- 固化 Product / SKU / FulfillmentRule / Order / PaymentEventInbox / FulfillmentExecution 模型。
- 明确旧 license order 与新 commerce order 的兼容策略。
- 明确 Creem 只能出现在 billing payment adapter / webhook 层。

验收标准：

- 文档包含目标架构、领域模型、API 草案、配置项和测试策略。
- #44 指向本 issue，权益网关与支付履约边界清晰。
- PC Core、frontend、mobile 不引入 Creem 概念。

### M6-B: Provider-agnostic Checkout Facade

范围：

- 新增 `CheckoutFacade` / `CheckoutService`。
- `PaymentProviderAdapter` 支持创建 checkout session，而不仅是 payment URL。
- 新订单路径支持 `user_id + sku_code`。

验收标准：

- mock provider 可创建 checkout session。
- 旧 `/orders` license 路径不受影响。
- 重复 idempotency key 返回同一 checkout/order。

### M6-C: Webhook Inbox 与幂等处理

范围：

- 新增 `PaymentEventInbox` repository/service。
- webhook handler 只做签名校验、入库、触发 processor。
- 重复 webhook 不重复履约。

验收标准：

- 同一 provider event 重放只处理一次。
- 签名失败不落入成功履约。
- 失败事件可后台重试或 admin reprocess。

### M6-D: FulfillmentService

范围：

- 根据 SKU 的 `FulfillmentRule` 发放 entitlement 和 credits。
- 通过 UnitOfWork 保证订单状态、grant、credit transaction、fulfillment execution 原子一致。
- 使用幂等 key 防止重复发放。

验收标准：

- 一个订单可同时产生 `editorial.studio` grant 和 credits grant。
- fulfillment 重试不会重复发放。
- 支付成功后 PC Core 刷新 snapshot 可看到新权益和 credits。

### M6-E: Creem Adapter

范围：

- 新增 Creem checkout adapter。
- 新增 Creem webhook verifier 和 event mapper。
- Creem product/price/customer/subscription id 只保存在 billing 内部。
- 新增 `PAYMENT_CREEM_*` 配置与 admin hot-reload 入口，SKU 到 Creem product ID 的映射保持配置化。

验收标准：

- Creem sandbox / test event 可完成 checkout_created -> paid -> fulfilled。
- PC Core 与 frontend 只看到 Walnut checkout URL、order 状态和 access snapshot。
- 静态检查确认非 billing payment 层不出现 `creem` 直接依赖。

### M6-F: PC Core Checkout CTA

范围：

- #44 的 `open_credits` / 后续 `open_checkout` CTA 只调用 PC Core facade。
- PC Core 再代理到 walnut-billing checkout facade。

验收标准：

- 前端组件仍只渲染 AccessDecision.cta。
- checkout 打开失败时不影响基础编辑部能力。
- 支付成功后通过 snapshot refresh 解锁，不由前端自行判断支付状态。


### M6-G: Refund / Cancel / Subscription Policy

业务决策：

- 退款窗口采用 7 天。
- 用户主动取消订阅后，已付费当前周期继续有效；取消不等于退款。
- 退款扣回 credits 时只扣当前可扣余额，不制造负余额。
- 订阅赠点按周期额度建模，长期不承诺永久累积；第一阶段先用 ledger source/idempotency 区分，后续升级为 credit bucket / expiry。
- 续费失败进入 3 天 grace period；grace period 内保留高级权益，但不发新周期点数。
- chargeback / dispute 作为高风险退款处理：立即撤销订单相关权益、扣回可扣 credits，并通过 `PaymentRiskFlag` 标记支付风险；用户级购买限制后续由 checkout policy 接入。

实现原则：

- `payment.refunded` / `payment.cancelled` 先进入 `PaymentEventInbox`，再由 policy service 处理。
- Creem 或未来海外渠道只提供支付事实，不直接撤销门禁。
- 撤销只基于 Walnut 自有记录：`Order`、`FulfillmentExecution.ResultRef`、`EntitlementGrant`、`CreditTransaction`、`CreditAccount`。
- 退款履约补偿必须幂等，重复 webhook 不重复扣点或反复修改 grant。

第一切片验收标准：

- `payment.cancelled` 对已 fulfilled 订阅订单不立即撤销 `editorial.studio`，只把订单标记为 cancelled/保持周期策略记录，门禁等 grant 自然到期。
- `payment.refunded` 立即撤销本订单 fulfillment 产生的 active grant。
- `payment.refunded` 扣回本订单 fulfillment 发放的 credits：最多扣当前 `CreditAccount.Balance`，不扣成负数。
- 重复退款 webhook 通过 idempotency key 保证不会重复扣回。
- handler / Creem adapter 不出现退款业务分支，全部由 policy service 决策。


## 当前推进状态

### M6-B 第一切片已完成：Provider-agnostic Checkout Facade

本轮先实现不依赖 Creem 的 checkout 基础设施，确保后续海外支付渠道只是 provider adapter，而不是反向污染订单、权益或客户端门禁。国内支付仅保留 legacy 兼容，不进入新商业化闭环。

已完成：

- 扩展 `Order` 模型，新增 `user_id`、`sku_code`、`provider_checkout_id`、`provider_customer_id`、`checkout_url`、`idempotency_key`、`fulfilled_at` 与 `checkout` order type。
- 新增 `payment.CheckoutRequest`、`payment.CheckoutSession`、`payment.CheckoutProvider`，保留旧 `PaymentProvider` 兼容路径。
- `PaymentService.CreateCheckoutSession()` 优先调用 hosted checkout provider；旧 WeChat/Alipay payment URL provider 仅保留兼容适配，不继续投入新能力。
- 新增 `CheckoutService` facade，负责校验 user/SKU、创建 Walnut 内部 checkout order、调用 payment gateway、回写 provider checkout 字段。
- 新增 dev-only `mock` checkout adapter，用于本地跑通 checkout flow，不引入 Creem。
- 新增 `POST /api/v1/commerce/checkout-sessions`，handler 只做 transport mapping，业务编排留在 service。
- checkout 使用 `idempotency_key` 保证重试返回同一订单，并拒绝同一 key 被不同 user/SKU/provider 复用。

验证：

```bash
go test ./...
```

M6-B 后续仍需补齐：

- SKU/FulfillmentRule 独立 catalog，不再复用 legacy `Product` 表承载所有商品语义。
- Checkout order list/query/admin 视图。
- PC Core 代理 checkout facade，前端仍只通过 AccessDecision CTA 触发。


### M6-C 第一切片已完成：Webhook Inbox 与幂等处理

本轮继续保持 provider-agnostic，不接入真实支付渠道。目标是把生产级支付闭环中最关键的 webhook 安全入口先固定下来。

已完成：

- 新增 `PaymentEventInbox` 模型，保存 provider、provider event id、event type、payload hash、签名校验结果、处理状态、attempts、last error 与 processed_at。
- 新增 `PaymentEventRepository` / `PaymentEventRepo`，通过 `provider + provider_event_id` 做幂等去重。
- 新增 `payment.WebhookVerifier`、`WebhookVerificationRequest`、`VerifiedWebhookEvent`，现代 JSON webhook provider 可实现专属 verifier；旧 callback provider 通过 `VerifyCallback` 兼容适配。
- 新增 `PaymentEventService`，集中处理 webhook 验证、inbox 入库、重复事件安全返回、失败重试、未知事件 ignored、签名失败拒绝入库。
- 新增 `PaymentOrderEventProcessor`，先把 `payment.paid` / `payment.cancelled` / `payment.refunded` 映射为 Walnut `Order` 状态；后续 M6-D 可用 FulfillmentService 装饰该 processor。
- 新增 `POST /api/v1/webhooks/:provider`，handler 只收集 headers/query/form/json/raw payload 并委托 service。
- 新增 admin 查询与重试入口：
  - `GET /api/v1/admin/payment-events`
  - `GET /api/v1/admin/payment-events/:id`
  - `POST /api/v1/admin/payment-events/:id/reprocess`

验证：

```bash
go test ./...
git diff --check
# 非 docs 区域无 creem/Creem 引用
```

M6-C 后续仍需补齐：

- 真实 provider 的签名 verifier 和 event mapper。
- 更严格的 raw payload 保存/脱敏策略与 payload 大小限制。
- 失败重试后台 worker / 指数退避 / 告警指标。
- 与 M6-D `FulfillmentService` 合并成 paid -> fulfilled 的完整事务闭环。


### M6-D 第一切片已完成：FulfillmentService 与 paid -> fulfilled 闭环

本轮继续保持 provider-agnostic，不接入真实支付渠道。目标是把支付成功后的 Walnut 内部履约路径收敛为稳定、可重试、可审计的生产级闭环。

已完成：

- 新增 `FulfillmentExecution` 模型，记录每个订单/规则的履约执行结果，通过 `idempotency_key` 防止重复发放。
- 新增 `FulfillmentExecutionRepository` / GORM 实现，支持 admin 查询、按订单/user/SKU/status 过滤。
- 新增 `FulfillmentService` facade，读取 Walnut paid checkout order，根据 `FulfillmentRule` 执行 entitlement 和 credits 履约。
- 新增 `FulfillmentRuleExecutor` 策略接口，当前内置：
  - `grant_entitlement`：发放稳定 entitlement，例如 `editorial.studio`。
  - `grant_credits`：发放 Walnut Credits ledger grant。
- 新增 `FULFILLMENT_RULES_JSON` 配置入口；dev defaults 包含 `editorial_studio_monthly` 与 `credits_600`，后续可迁移到 DB catalog。
- 扩展 UnitOfWork，把 order、user、entitlement grant、credit account/transaction、fulfillment execution 放入同一事务边界。
- 新增 `PaymentFulfillmentEventProcessor`，以 decorator/composition 方式在 `payment.paid` 更新订单后触发履约；webhook handler 仍只做 transport mapping。
- 新增 admin audit endpoint：`GET /api/v1/admin/fulfillments`。
- entitlement 月度履约以现有 active grant 的最大 `expires_at` 为续期锚点，避免重复购买时有效期重叠浪费。

生产级控制点：

- 支付 provider 状态不直接门禁；门禁仍只读 `EntitlementGrant` / `CreditTransaction` 生成的 snapshot。
- 同一 payment event 重放先由 inbox 去重；同一 fulfillment retry 再由 execution/target idempotency 双层去重。
- 事务失败时回滚 grant/credits/order fulfilled，避免半发放；失败 execution 会在外层持久化用于诊断和 reprocess。
- 旧 license order 仍走 legacy `/orders` 路径；新 commerce checkout 只处理 `OrderTypeCheckout`。

验证：

```bash
go test ./...
git diff --check
# 非 docs 区域无 creem/Creem 引用
```

M6-D 后续仍需补齐：

- 将 `FULFILLMENT_RULES_JSON` 迁移为 versioned DB catalog / admin API。
- refund/cancel policy：是否 revoke entitlement、扣回未使用 credits、是否允许负余额。
- 后台 retry worker / 指数退避 / 告警指标；当前已支持 admin reprocess。

### M6-E 第一切片已完成：Creem Adapter skeleton + checkout/webhook mapper

本轮接入官方 Creem 文档中已确认的稳定边界：`POST /v1/checkouts`、`x-api-key`、`request_id`、`checkout.completed` webhook，以及 `creem-signature` HMAC-SHA256 签名。实现仍然保持 provider adapter 可替换，不让 Creem 进入 PC/mobile/core 门禁。

已完成：

- 新增 `payment.CreemAdapter`，实现 `PaymentProvider`、`CheckoutProvider`、`WebhookVerifier`。
- checkout 创建使用 `product_id`、`request_id`、`success_url`、`customer`、`metadata`，并把 Walnut `out_trade_no/user_id/sku_code/idempotency_key` 写入 metadata 用于回查。
- webhook verifier 校验 `creem-signature`，再把 `checkout.completed` 归一化为 Walnut `payment.paid`。
- webhook mapper 支持从 `object.request_id`、`object.checkout.request_id`、`metadata.walnut_out_trade_no` 提取 Walnut `out_trade_no`。
- 新增 `PAYMENT_CREEM_API_KEY`、`PAYMENT_CREEM_WEBHOOK_SECRET`、`PAYMENT_CREEM_SANDBOX`、`PAYMENT_CREEM_PRODUCT_MAP_JSON` 等配置。
- 新增 admin hot-reload：`PUT /api/v1/admin/payment/creem`。
- `CheckoutRequest` 增加 customer email/name，让 hosted checkout 可预填客户信息。
- `Product` 增加 `currency`，海外商业化 SKU 默认 `USD`；payment paid 事件需要 amount 与 currency 同时匹配 Walnut order 才能履约。

当前边界：

- Creem product id 只存在于 `walnut-billing` 的 provider adapter/config 中；客户端仍提交 Walnut `sku_code`。
- Creem event 只进入 `PaymentEventInbox`；真正发放权益仍由 M6-D `FulfillmentService` 写入 `EntitlementGrant` 和 `CreditTransaction`。
- 退款/订阅取消当前只做事件归一化准备，最终 revoke/扣回策略仍需单独定义。

验证：

```bash
go test ./...
```

### M6-G 第一切片已完成：Refund / Cancel policy service

本轮先把已拍板的业务策略落成独立 policy service，避免在 Creem adapter、webhook handler 或订单 processor 中堆退款分支。

已完成：

- 新增 `PaymentAdjustmentService`，专门处理 `payment.refunded` / `payment.cancelled` 的 Walnut 内部补偿策略。
- `payment.cancelled` 遵守“当前已付费周期继续有效”：不撤销当前 `editorial.studio` grant，不扣回 credits。
- `payment.refunded` 通过 `FulfillmentExecution.ResultRef` 回溯本订单发放的 grant / credit transaction：
  - active entitlement grant 立即标记为 `revoked`，并把 `expires_at` 收敛到当前时间。
  - credits 只从当前可用 `CreditAccount.Balance` 中扣回，不制造负余额。
  - 即使余额为 0，也写入 0 金额 `clawback` ledger marker，避免重复退款 webhook 在用户未来充值后误扣新余额。
- 新增 `CreditTransactionTypeClawback`，保持账本不可变，不直接删除历史 grant。
- `PaymentFulfillmentEventProcessor` 通过 composition 接入 adjustment service；provider adapter 和 webhook handler 仍只负责事实归一化。

验证：

```bash
go test ./...
git diff --check
```

### M6-G 第二切片已完成：Payment risk flag / dispute baseline

本轮补齐 chargeback / dispute 的生产级基础设施：高风险支付事件先进入 Walnut 自己的风险标记模型，再通过既有 adjustment policy 执行撤销和 credits 扣回。风险标记只作为审计和后续 checkout policy 输入，不直接成为 PC/mobile 门禁条件。

已完成：

- 新增 provider-agnostic 事件类型 `payment.disputed`。
- 新增 `PaymentRiskFlag` 模型和 `PaymentRiskFlagRepository` / GORM 实现。
- `PaymentRiskFlag` 通过 `(provider, provider_event_id)` 幂等，支持多海外渠道并存。
- `PaymentAdjustmentService` 处理 `payment.disputed` 时：
  - 创建 open / critical 风险标记。
  - 复用退款补偿策略撤销本订单 grant。
  - 扣回可扣 credits，仍不制造负余额。
- Creem adapter 只做事件归一化：`dispute.created` / `chargeback.created` -> `payment.disputed`，不包含风控业务分支。
- `PaymentFulfillmentEventProcessor` 通过 composition 接入 disputed 处理，webhook handler 仍保持 provider-agnostic。

验证：

```bash
go test ./...
git diff --check
```

### M6-G 第三切片已完成：Checkout risk policy / purchase hold

本轮把第二切片产生的 `PaymentRiskFlag` 接入 checkout 前置策略，形成“支付事实 -> Walnut 风险标记 -> 购买控制”的闭环。该策略只阻断新的 checkout session，不直接改写 PC/mobile 的 access snapshot，也不让 Creem 或未来海外渠道进入门禁判断。

已完成：

- 新增 `CheckoutPolicy` 策略接口与 `CheckoutPolicyDecision`，`CheckoutService` 通过 composition 执行前置策略，保持 checkout facade 的扩展点清晰。
- 新增 `PaymentRiskCheckoutPolicy`：当用户存在 open / high 或 open / critical `PaymentRiskFlag` 时，阻断 `/commerce/checkout-sessions`。
- 风险阻断发生在 Walnut order 创建和 provider checkout 调用之前，避免高风险用户生成新订单或触达海外支付渠道。
- API 对风险阻断返回稳定的 `403`、`code=checkout_blocked_by_payment_risk`、`action=manual_review`，方便后续接入用户登记、人工审核和 admin 风险处理台。
- 支持 `CHECKOUT_RISK_POLICY_ENABLED` 和 `CHECKOUT_RISK_BLOCK_SEVERITIES`，默认阻断 open / critical 与 open / high 风险标记；后续可平滑迁移到 DB policy。
- `cmd/server` 只注入 Walnut 自有 `PaymentRiskFlagRepository`，Creem adapter / webhook handler 仍只做支付事实归一化。

验证：

```bash
go test ./...
git diff --check
```

M6-G 后续仍需补齐：

- 7 天退款窗口、低使用条件、人工审核策略还未落 DB policy。
- 订阅赠点 bucket / expiry 尚未实现，当前通过 ledger source/idempotency 区分。
- admin 风险处理视图：列出、备注、解决 `PaymentRiskFlag`。
- 续费失败 3 天 grace period 需要结合 provider subscription renewal event 单独处理。

## 测试策略

- Unit tests：catalog rule 解析、provider adapter、event mapper、fulfillment rule executor。
- Repository tests：order、event inbox、fulfillment execution 的唯一键和状态流转。
- Service tests：重复 webhook、重复履约、退款/取消事件、credits grant 幂等。
- Integration tests：mock provider 完整跑通 checkout -> webhook -> order paid -> fulfillment -> snapshot。
- Static checks：PC Core / frontend / mobile 不出现 Creem SDK 或 Creem API 直连。

## 风险与控制

| 风险 | 控制方式 |
|---|---|
| 支付状态直接驱动门禁 | 只允许 fulfillment 写入 `EntitlementGrant` / `CreditTransaction`，门禁只读 snapshot |
| webhook 重放导致重复发点 | `PaymentEventInbox` + `FulfillmentExecution` 双层幂等 |
| 商品配置硬编码 | SKU 与 FulfillmentRule 配置化，支持 JSON seed 迁移到 DB |
| 旧 license 购买路径被破坏 | 新 commerce checkout 与旧 `/orders` 路径并行，逐步迁移 |
| Creem 字段扩散到客户端 | Creem 只存在于 billing adapter / webhook 层，PC Core 只看 Walnut facade |
| 退款和订阅取消语义复杂 | 先定义 policy，代码按 rule executor 扩展，不在 webhook handler 写分支 |

## 开放问题

1. Creem 是否作为 Merchant of Record 覆盖目标销售地区、税务和发票要求？
2. 首期商品是否只做点数包，还是同时上线编辑部工作室月度包？
3. 订阅赠点是“每周期发放并过期”，还是进入永久余额？
4. 退款时是否扣回未使用 credits，已使用部分是否允许负余额？
5. 团队版 seats 和共享额度池是否进入 M6，还是延后到 M7？

## 当前建议

先推进 M6-A 到 M6-D 的 provider-agnostic 基础，再接 M6-E Creem。这样 Creem 只是一个 adapter，不会反向污染 Walnut 的权益、点数和编辑部业务边界。
