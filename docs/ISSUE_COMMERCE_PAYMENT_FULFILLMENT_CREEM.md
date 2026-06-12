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

## 当前架构评估

`walnut-billing` 已经具备部分可复用基础：

| 现有能力 | 当前职责 | M6 复用方式 |
|---|---|---|
| `Product` | 旧 license 商品 | 演进为商品目录的兼容层，后续拆出 `SKU` / `FulfillmentRule` |
| `Order` | 支付订单，当前强绑定 `LicenseKey` | 保留旧路径，新增用户维度和 SKU/checkout 维度 |
| `PaymentProvider` | WeChat / Alipay 适配器接口 | 升级为 provider-agnostic checkout + webhook adapter |
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
      -> PaymentProviderAdapter(creem | wechat | alipay | mock)
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
| `PaymentProviderAdapter` | Strategy / Adapter | 屏蔽 Creem、WeChat、Alipay 差异 |
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


## 当前推进状态

### M6-B 第一切片已完成：Provider-agnostic Checkout Facade

本轮先实现不依赖 Creem 的 checkout 基础设施，确保后续 Creem 只是 provider adapter，而不是反向污染订单、权益或客户端门禁。

已完成：

- 扩展 `Order` 模型，新增 `user_id`、`sku_code`、`provider_checkout_id`、`provider_customer_id`、`checkout_url`、`idempotency_key`、`fulfilled_at` 与 `checkout` order type。
- 新增 `payment.CheckoutRequest`、`payment.CheckoutSession`、`payment.CheckoutProvider`，保留旧 `PaymentProvider` 兼容路径。
- `PaymentService.CreateCheckoutSession()` 优先调用 hosted checkout provider；旧 WeChat/Alipay payment URL provider 可被适配成 checkout session。
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
