# Issue: walnut-billing 商业控制平面开发计划

状态：In Progress  
适用仓库：`walnut-billing`  
更新日期：2026-06-19

## 1. 目标

把 `walnut-billing` 从“授权 + 支付服务”演进为 Walnut 的 **商业控制平面（Commercial Control Plane）**：统一管理用户身份、设备、试用、软件授权、checkout、支付事件、订阅生命周期、后台管理、云存储配额与同步元数据。

核心判断：这三类能力可以放在 `walnut-billing` 中实现，但必须按模块化单体推进，而不是把所有逻辑堆进 handler 或支付回调中。

```text
Walnut App / PC Core
  -> walnut-billing control plane
    -> identity / access / commerce / cloud-storage / admin / audit
      -> provider adapters: payment, object storage, email, future services
```

### 1.1 产品闭环目标

| 闭环 | 用户侧结果 | 服务端事实 | 当前优先级 |
|---|---|---|---:|
| 首次打开 | 输入邮箱后自动注册/登录，获得试用或恢复已有授权 | `User + UserDevice + TrialGrant + AccessSnapshot` | P0 |
| 付费升级 | Basic 可升级 Pro 月付或终身；购买后立刻刷新授权 | `Order + PaymentEventInbox + FulfillmentExecution + EntitlementGrant` | P0 |
| 订阅管理 | 月付可退订/恢复；终身与月付互斥；展示清晰到期状态 | `SoftwareSubscription` 投影 + `SubscriptionCancellation` | P0 |
| 授权恢复 | 换设备、重装、清本地数据后通过邮箱登录恢复 | `User + Device binding + signed snapshot` | P0 |
| 管理后台 | 管理员可查看脱敏账号、订单、授权、设备、风险、云配额 | RBAC admin APIs + audit logs | P1 |
| 云存储 | App 直传对象存储，billing 只管配额、manifest、metadata | `CloudProject + CloudManifest + CloudObject` | P1 |
| 风险控制 | 退款/争议/异常设备不直接破坏本地基础功能，但限制商业能力 | `PaymentRiskFlag + policy` | P1 |

### 1.2 非目标

| 非目标 | 原因 |
|---|---|
| 不在 billing DB 中保存 Wiki、稿件、素材正文或二进制文件 | billing 是控制平面，不是内容仓库 |
| 不让 Walnut App 依赖 Creem product id、支付状态或 SKU | App 只消费 Walnut 自有授权快照 |
| 不把云存储实现成 billing 文件代理 | 大文件应由 App 直传对象存储；billing 只签发 upload target 和记录 metadata |
| 不在当前阶段售卖 hosted AI 点数 | 当前商业方案是 BYOK-first；AI compute 与软件授权分离 |
| 不用支付平台订阅状态直接做门禁 | 门禁只看 `EntitlementGrant` / signed access snapshot |
| 不在未确定云厂商前写 OSS/S3/R2 的伪实现 | 先保留 provider interface + unconfigured provider，待 ADR 决策后接入 |

## 2. 当前实现基线

`walnut-billing` 已经具备可继续演进的基础：

| 能力 | 当前代码位置 | 评价 |
|---|---|---|
| 服务启动、DI、路由挂载 | `cmd/server/main.go` | 功能完整，但 DI 越来越长，需要拆成 module bootstrap |
| 配置 | `internal/config/config.go` | 已覆盖 access、Creem、checkout、renewal、cloudstorage；需要生产校验分层 |
| 支付适配器 | `internal/payment/*` | 已采用 Adapter / Registry，适合扩展 Creem 与未来 provider |
| Checkout facade | `internal/service/checkout.go` | 已实现 provider-neutral checkout session 与 checkout policy |
| Webhook inbox | `internal/service/payment_event.go` | 已有 provider-agnostic inbox、幂等和 reprocess 基础 |
| 履约 | `internal/service/fulfillment.go` | 已将支付成功投影为 entitlement / credits，方向正确 |
| Access session | `internal/service/access_session.go` | 已支持邮箱注册/恢复、设备、trial、snapshot issuance |
| Signed snapshot | `internal/service/access_snapshot.go` | 已支持 snapshot v2、TTL、offline grace、签名 |
| 订阅取消/恢复 | `internal/service/subscription_cancellation.go` | 已接 provider-first subscription control，并保留 Walnut 自有取消/恢复事实 |
| 云存储控制面 | `internal/service/cloud_storage.go` | 已有 provider interface、quota、manifest、object metadata；provider 未配置时显式失败 |
| 管理后台 | `internal/api/handler/*admin*`, `internal/web/static/index.html` | 已有 API key/RBAC、脱敏账号、dashboard shell；需要用户/订单/云存储运营面完善 |
| 可重复测试环境 | `scripts/run_deterministic_billing.sh`, `docs/LOCAL_COMMERCE_TEST_ENV.md` | 已具备 mock profile；需要补齐 Creem sandbox 与测试数据 reset 策略 |

### 2.1 当前主要设计债

| 设计债 | 风险 | 处理原则 |
|---|---|---|
| `cmd/server/main.go` 负责过多 DI、路由和 provider 注册 | 新模块继续增加会导致启动逻辑不可维护 | 拆出 `internal/app/bootstrap` 与 module registrars |
| `internal/service` 下文件按技术层聚合，领域边界不直观 | 后续 identity/access/commerce/cloud 相互引用容易失控 | 先定义 module contracts，再逐步物理拆包 |
| 订阅状态主要由订单、grant、取消记录推导 | UI 展示、恢复、互斥判断容易散落 | 新增 `SoftwareSubscription` 投影，由事件驱动更新 |
| Admin dashboard 还偏工具页 | 无法支持真实运营排查和权限分工 | 建 admin read models + RBAC permission matrix |
| Cloud storage provider 未定 | 过早实现容易变成假代码 | 先写 ADR 和 adapter contract tests，provider 选择后再落地 |
| Creem test mode 未形成强验收闭环 | 真实支付链路不可重复验证 | 固化 sandbox runbook、webhook replay、subscription event fixtures |

## 3. 目标架构

### 3.1 模块化单体布局

短期不做一次性大搬家，先在现有 layered package 内收敛边界；达到 WCP-0 后，再按 bounded context 做低风险物理拆包。

目标逻辑模块：

```text
walnut-billing
  ├── identity          # 用户、邮箱登录/恢复、设备绑定、设备撤销
  ├── access            # trial、entitlement、signed snapshot、离线宽限
  ├── commerce          # catalog、checkout、payment events、fulfillment、subscription、risk
  ├── cloud_storage     # project、manifest、object metadata、quota、upload session
  ├── admin             # 管理后台、RBAC、运营 read models、人工动作
  ├── audit             # 不可变审计、隐私投影
  └── observability     # metrics、structured logs、runbook health checks
```

建议演进后的物理目录：

```text
internal/
  app/
    bootstrap/              # 组装 config、db、repositories、module registrars、router
  api/
    http/                   # handler DTO、middleware、error mapping
  module/
    identity/
    access/
    commerce/
    cloudstorage/
    admin/
  platform/
    payment/                # Creem/mock/legacy provider adapters
    objectstorage/          # S3/OSS/R2/MinIO adapters after ADR
    email/                  # OTP/magic-link provider adapters
    crypto/                 # snapshot signer, token hashing
  repository/
    gorm_repo/
  domain/                   # 跨模块稳定实体和值对象
```

迁移策略：

1. WCP-0 先保留现有代码位置，新增模块边界文档与接口。
2. 新功能只通过 module service / ports 增加，不再扩大 `cmd/server/main.go`。
3. 每完成一个里程碑，把相关 handler/service/repository 的组装迁移到 module registrar。
4. 物理拆包必须无行为变化，并配套架构测试，禁止顺手修改业务规则。

### 3.2 依赖规则

```text
api/http handlers
  -> module application services
    -> module ports/interfaces
      -> repository + platform adapters
```

允许依赖：

| 模块 | 可依赖 | 禁止依赖 |
|---|---|---|
| `identity` | audit、repository、email adapter、clock/id | commerce、cloud storage、payment provider |
| `access` | identity read port、entitlement repos、snapshot signer | SKU、Creem、checkout URL、object storage provider |
| `commerce` | identity read port、access grant writer port、payment adapter、audit | Walnut App UI、云对象二进制、直接写 snapshot cache |
| `cloud_storage` | identity read port、access entitlement checker、object storage adapter、quota policy | 支付 provider、稿件/Wiki/素材内容解析 |
| `admin` | 各模块 admin facade、audit、privacy projector | 直接绕过服务写数据库 |
| `audit` | principal/privacy projector | 业务模块内部状态机 |
| `observability` | domain events / wrappers | 改写业务决策 |

### 3.3 核心请求流

#### 首次注册 / 恢复授权

```text
Walnut App
  -> POST /api/v1/access/registrations
    -> Identity.RegisterOrLogin(email, device_id)
      -> upsert User
      -> bind / restore UserDevice
      -> issue one-time trial when eligible
    -> Access.IssueSnapshot(user_id, device_id)
      -> signed AccessSnapshotV2
  <- user + device + trial + snapshot
```

#### 付费购买 / 履约

```text
Walnut App
  -> POST /api/v1/commerce/checkout-sessions
    -> CheckoutPolicy[]
      -> LifetimeMonthlyExclusivityPolicy
      -> PaymentRiskCheckoutPolicy
    -> Order(checkout_created)
    -> PaymentProviderAdapter.CreateCheckoutSession

Payment Provider Webhook
  -> POST /api/v1/webhooks/:provider
    -> Verify signature
    -> PaymentEventInbox(provider_event_id unique)
    -> PaymentEventProcessor
      -> Order state transition
      -> FulfillmentService
        -> EntitlementGrant(editorial.studio, cloud.storage)
        -> SoftwareSubscription projection
      -> Access snapshot refresh on next client request
```

#### 云同步

```text
Walnut App / PC Core
  -> scan local project and build resource manifest
  -> POST /api/v1/cloud-storage/sync-sessions
    -> check cloud.storage entitlement
    -> check quota
    -> ObjectStorageProvider.BuildUploadTarget
  -> direct upload to object storage
  -> POST /api/v1/cloud-storage/manifests
    -> commit CloudManifest + CloudObject metadata
  -> GET latest manifest during restore
```

## 4. 模块设计

### 4.1 Identity：账号、邮箱登录/恢复、设备

| 项 | 设计 |
|---|---|
| 职责 | 维护稳定用户、邮箱登录/恢复、设备绑定、设备撤销、设备限制 |
| 已有实体 | `User`, `UserDevice`, `TrialGrant` |
| 建议新增实体 | `AccessLoginChallenge`：OTP/magic-link challenge，存 hash、过期时间、attempts、consumed_at |
| 主要服务 | `IdentityService`, `DeviceBindingService`, `AccessRecoveryService` |
| Provider ports | `EmailSender`, `TokenHasher`, `Clock`, `IDGenerator` |
| Client API | `POST /api/v1/access/registrations`, `POST /api/v1/access/login-challenges`, `POST /api/v1/access/login-challenges/verify` |
| Admin API | `GET /api/v1/admin/users`, `GET /api/v1/admin/users/:id`, `GET /api/v1/admin/users/:id/devices`, `POST /api/v1/admin/devices/:id/revoke` |
| 数据隐私 | list 接口默认返回 `email_masked`, `email_domain`, `email_fingerprint`；详情接口需更高权限 |

验收标准：

- 同一邮箱重复注册不创建重复用户，返回同一个 `user_id`。
- 新设备登录受 `max_devices` 策略限制；被撤销设备不能刷新 Pro snapshot。
- 删除本地数据后，用户通过邮箱验证可恢复已有月付/终身授权。
- OTP/magic link 只存 hash，不在数据库保存明文验证码。
- 登录 challenge 有过期、次数限制、幂等消费保护。
- 所有设备绑定、撤销、登录失败超过阈值都写 audit。

### 4.2 Access：软件授权、试用、签名快照

| 项 | 设计 |
|---|---|
| 职责 | 发放 trial、维护 stable entitlement、签发 AccessSnapshotV2、离线宽限、授权降级 |
| 已有实体 | `EntitlementGrant`, `TrialGrant`, `AccessSnapshotV2` |
| 稳定权益 | `editorial.studio`, `cloud.storage`; `ai.hosted` 保留未来，不默认发放 |
| 主要服务 | `AccessSessionService`, `EntitlementService`, `AccessSnapshotIssuer`, `AccessPolicy` |
| Client API | `GET /api/v1/users/:user_id/access/snapshot`, `GET /api/v1/users/:user_id/entitlements/snapshot` |
| Admin API | `GET /api/v1/admin/grants`, `POST /api/v1/admin/grants`, `POST /api/v1/admin/grants/:id/revoke` |
| 签名策略 | dev 可 HS256；prod 必须 Ed25519/EdDSA 或等价非共享密钥策略 |

验收标准：

- 首次邮箱注册可按配置获得一次试用 Pro Own AI 权益；重复注册不会重复延长试用。
- App feature gate 不读取 SKU、价格、Creem 状态，只读取 signed snapshot 中的 stable entitlement。
- snapshot 包含 `issued_at`, `expires_at`, `offline_grace_until`, `signature_key_id`, `signature_algorithm`。
- billing 不可用时，App 可在 snapshot grace 内继续 Pro；超过 grace 自动降级 Basic。
- `software.advanced`、`workflow.batch_cleanup`、`workflow.advanced` 在未接入真实门禁前不进入默认 snapshot。
- 测试覆盖：签名校验失败、过期、设备撤销、用户 disabled、grant expired/revoked。

### 4.3 Commerce Catalog / Checkout：商品、购买入口、互斥策略

| 项 | 设计 |
|---|---|
| 职责 | 定义可售 SKU、发起 checkout、执行购买前策略、屏蔽 provider 细节 |
| 已有实体 | `Product`, `Order` |
| SKU | `basic_own_ai` plan；`pro_own_ai_monthly`；`pro_own_ai_lifetime` |
| 主要服务 | `CommerceCatalog`, `CheckoutService`, `CheckoutPolicy[]`, `ProductCatalogReconciler` |
| Provider ports | `PaymentProviderAdapter`, `PaymentProviderRegistry` |
| Client API | `POST /api/v1/commerce/checkout-sessions` |
| Admin API | `GET /api/v1/admin/orders`, `GET /api/v1/admin/payment/providers` |

必须实现的 checkout policy：

| Policy | 规则 | 机器可读 reason |
|---|---|---|
| `LifetimeOwnsProPolicy` | 已有终身 Pro 时禁止购买月付和重复购买终身 | `already_lifetime` |
| `ActiveMonthlyPolicy` | 月付 active 时禁止重复创建月付 checkout，UI 应展示退订/恢复 | `subscription_active` |
| `CancelAtPeriodEndPolicy` | 月付已退订但仍在当前周期内，可 resume，不创建重复 checkout | `cancel_at_period_end` |
| `PaymentRiskCheckoutPolicy` | 高风险用户阻断 checkout，需要人工处理 | `payment_risk_hold` |
| `IdempotencyPolicy` | 同 idempotency key 返回相同订单，不重复创建 | `idempotent_replay` |

验收标准：

- Basic 用户可创建 `pro_own_ai_monthly` / `pro_own_ai_lifetime` checkout。
- 终身用户再次点击购买终身版时返回明确状态，不创建新订单。
- 月付 active 用户点击“升级月付”不会创建新订单，客户端可据 reason 改为“退订”。
- 使用相同 `idempotency_key` 重试 checkout 返回同一 `out_trade_no`。
- checkout response 不泄露 Creem product id，只返回 Walnut order、provider 和 checkout URL。
- 商品 catalog 的默认履约只包含 `editorial.studio` 与 `cloud.storage`。

### 4.4 Payment Events / Fulfillment：Webhook、履约、退款争议、订阅

| 项 | 设计 |
|---|---|
| 职责 | 接收 provider webhook，去重，处理订单状态，执行履约，处理退款/争议/续费/过期 |
| 已有实体 | `PaymentEventInbox`, `FulfillmentExecution`, `PaymentRiskFlag`, `SubscriptionCancellation` |
| 建议新增实体 | `SoftwareSubscription`：Walnut 自有订阅投影，不直接等于 provider subscription |
| 主要服务 | `PaymentEventService`, `PaymentFulfillmentEventProcessor`, `FulfillmentService`, `PaymentAdjustmentService`, `SubscriptionRenewalService`, `SubscriptionCancellationService` |
| Provider ports | `WebhookVerifier`, `SubscriptionControlProvider`（可选接口） |
| Client API | `POST /api/v1/commerce/subscriptions/cancel`, `POST /api/v1/commerce/subscriptions/resume` |
| Admin API | `GET /api/v1/admin/payment-events`, `POST /api/v1/admin/payment-events/:id/reprocess`, `GET /api/v1/admin/fulfillments`, `GET /api/v1/admin/payment-risk-flags` |

`SoftwareSubscription` 建议字段：

| 字段 | 说明 |
|---|---|
| `id` | `sub_` 前缀服务端 ID |
| `user_id` | Walnut 用户 |
| `sku_code` | 当前订阅 SKU，初期只支持 `pro_own_ai_monthly` |
| `provider` | `creem` / `mock` |
| `provider_subscription_id` | provider subscription id，允许为空用于 mock |
| `status` | `active`, `cancel_at_period_end`, `past_due`, `expired`, `cancelled` |
| `current_period_start_at` | 当前周期开始 |
| `current_period_end_at` | 当前周期结束，也是 UI 展示“服务可用至”的依据 |
| `cancel_at_period_end` | 是否到期后取消 |
| `latest_order_no` | 最近一次成功支付订单 |
| `updated_by_event_id` | 最近 webhook 事件 |

Provider cancellation 设计：

```text
SubscriptionCancellationService
  -> SubscriptionControlProvider if adapter supports it
    -> Creem cancel/resume subscription
  -> always write Walnut cancellation/subscription projection
  -> next snapshot uses Walnut projection + grants
```

验收标准：

- mock provider paid webhook 后，`Order -> PaymentEventInbox -> FulfillmentExecution -> EntitlementGrant -> AccessSnapshot` 全链路成功。
- Creem sandbox checkout 能创建真实 test checkout URL；Creem webhook 签名校验通过后进入同一履约链路。
- 同一 `provider + provider_event_id` 重放 webhook，不重复发放 grant。
- 月付 paid/renewal paid 使用 provider period end，不出现“月付不到一个月”或时区误读。
- renewal failed 只进入短期 grace，不发放 hosted AI credits。
- subscription expired 到期后 Pro 权益过期，Basic 仍可用。
- cancel at period end 后，当前周期内继续 Pro；resume 后取消标记清除。
- lifetime 与 monthly 互斥：lifetime 生效后，monthly checkout 被拒绝；如已有 monthly，后续策略明确是否自动 cancel 或提示用户先退订。
- refund/dispute 进入 `PaymentRiskFlag`，阻断新 checkout，并通过 admin resolve 恢复。

### 4.5 Cloud Storage Control Plane：云存储控制面

| 项 | 设计 |
|---|---|
| 职责 | 校验 `cloud.storage` 权益、配额、项目、manifest、object metadata、上传授权、恢复清单 |
| 已有实体 | `CloudProject`, `CloudManifest`, `CloudObject` |
| 建议新增实体 | `CloudSyncSession`：记录 upload session、过期、requested bytes、commit 状态 |
| 主要服务 | `CloudStorageService`, `CloudQuotaPolicy`, `ObjectStorageProvider` |
| Provider ports | `ObjectStorageProvider.BuildUploadTarget`, 后续 `BuildDownloadTarget`, `DeleteObject` |
| Client API | `POST /api/v1/cloud-storage/sync-sessions`, `POST /api/v1/cloud-storage/manifests`, `GET /api/v1/users/:user_id/cloud-storage/usage`, `GET /api/v1/users/:user_id/cloud-storage/projects`, `GET /api/v1/cloud-storage/projects/:id/manifests/latest` |
| Admin API | `GET /api/v1/admin/cloud-storage/usage`, `GET /api/v1/admin/users/:id/cloud-storage/projects` |

Provider ADR 决策前只保留接口与 unconfigured provider：

| Provider 选项 | 需要评估 |
|---|---|
| S3-compatible / Cloudflare R2 | 全球可用性、成本、签名 URL、生命周期管理 |
| 阿里云 OSS | 国内可用性、海外访问、合规和成本 |
| 自建 MinIO | 运维成本、备份、可用性、安全责任 |
| Supabase / managed storage | 开发速度、锁定风险、权限模型 |

验收标准：

- 无 `cloud.storage` entitlement 时，sync session 返回 403。
- Provider 未配置时，明确返回 409 `cloud storage provider not configured`，不伪造成功。
- 超配额返回 402/业务错误，并给出 used/quota/requested 信息。
- object key 不包含本地绝对路径，只由 user/project/resource/content hash 等稳定字段生成。
- App 直传对象存储，billing 不接收文件 bytes。
- manifest commit 幂等：同一 idempotency key 不重复写 object metadata。
- restore API 能返回用户项目列表与最新 manifest，App 可据此重建项目文件清单。
- 云删除、用户删除、项目归档必须写 audit，并遵循 retention policy。

### 4.6 Admin：管理后台和运营工具

| 项 | 设计 |
|---|---|
| 职责 | 账号/设备/授权/订单/支付事件/风险/云配额查询和人工操作 |
| 已有能力 | API key + permission middleware、dashboard shell、access account masked view、user access summary read model、admin order read model |
| 主要服务 | `AccessAdminService`, `AdminUserAccessSummaryService`, `AdminOrderService`, `AdminCloudFacade`, `AdminAuditFacade` |
| 认证 | dev 可 API key；prod 使用 scoped principals，后续可接 OIDC/SSO |
| 权限 | support read、ops write、finance payment、admin all |
| 隐私 | 默认脱敏；高权限详情也记录 audit；禁止导出明文敏感数据 |

Admin 页面分区：

| 页面 | 核心功能 | 权限 |
|---|---|---|
| Dashboard | 今日 checkout、paid、failed webhook、risk、active users、cloud usage | `admin.dashboard.read` |
| Users | 搜索脱敏邮箱/指纹、查看用户详情、设备、snapshot 摘要 | `admin.users.read` |
| Access | 查看/创建/撤销 grants，查看 trial 和 device 状态 | `admin.access.write` |
| Commerce | 订单、checkout、subscription、refund/dispute 状态 | `admin.orders.read`, `admin.payment_events.read` |
| Payment Events | webhook inbox、payload hash、reprocess | `admin.payment_events.write` |
| Risk | risk flags、resolve、备注 | `admin.payment_risk.write` |
| Cloud Storage | 用户配额、项目、manifest、object metadata，不展示文件正文 | `admin.cloud_storage.read` |
| Audit | 操作审计、隐私投影 | `admin.audit.read` |

验收标准：

- 管理后台可查看“登记邮箱的脱敏信息”，不可在列表泄露 raw email。
- support-key 只能读脱敏数据，不能 grant、resolve risk、reprocess webhook。
- ops-key 可执行管理动作，所有动作写 audit，并记录 principal。
- 管理员能定位：某邮箱是否注册、绑定了哪些设备、当前授权来自 trial/monthly/lifetime、到期时间、是否 cancel at period end。
- 管理员能复测完整商业链路：创建测试用户、mock checkout、simulate paid、查看履约、刷新 snapshot。
- dev/test-only reset 能力必须受 `SERVER_ENV != prod` 和专用 permission 双重保护。

### 4.7 Audit / Observability / Security

| 模块 | 设计 |
|---|---|
| Audit | 所有人工 grant、撤销、设备 revoke、risk resolve、webhook reprocess、cloud delete 写入不可变审计 |
| Observability | metrics + structured logs + request id；关键状态机有 counters/histograms |
| Security | admin RBAC、rate limit、webhook signature、snapshot signing、secret redaction、payload hash |
| Privacy | 邮箱脱敏、fingerprint 可搜索、raw payload 裁剪或加密、日志不打印 secret |

验收标准：

- `/metrics` 暴露 checkout、payment event、fulfillment、subscription、cloud sync、admin action 指标。
- webhook 签名失败不进入履约；重复事件不增加履约次数。
- prod 启动时缺少 admin auth、snapshot prod signer、Creem webhook secret 时直接失败或进入只读安全模式。
- 所有错误有稳定 code，客户端不需要解析英文错误文案。

## 5. 数据模型归属

| 表 / 实体 | 归属模块 | 说明 |
|---|---|---|
| `users` | identity | 稳定账号 |
| `user_devices` | identity | 设备绑定、状态、限制 |
| `access_login_challenges` | identity | 建议新增，邮箱登录/恢复 OTP/magic link |
| `trial_grants` | access | 试用发放事实，防重复试用 |
| `entitlement_grants` | access | 最终软件门禁事实 |
| `products` | commerce catalog | 可售 SKU 兼容表，后续可拆 `skus` |
| `orders` | commerce | Walnut 自有订单事实 |
| `payment_event_inboxes` | commerce | provider webhook inbox |
| `fulfillment_executions` | commerce | 履约幂等记录 |
| `software_subscriptions` | commerce/access projection | 建议新增，订阅展示和互斥策略的 read model |
| `subscription_cancellations` | commerce | 退订/恢复事实 |
| `payment_risk_flags` | commerce risk | 退款/争议/异常风险 |
| `credit_*` | future hosted AI / ledger | 当前 BYOK 不对用户展示 |
| `cloud_projects` | cloud_storage | 用户项目锚点 |
| `cloud_manifests` | cloud_storage | 同步提交事实 |
| `cloud_objects` | cloud_storage | 对象 metadata，不保存 bytes |
| `cloud_sync_sessions` | cloud_storage | 建议新增，上传授权会话 |
| `audit_entries` | audit | 隐私投影审计 |

## 6. API 合约规划

### 6.1 Client / Walnut App API

| Method | Path | 归属 | 状态 |
|---|---|---|---|
| `POST` | `/api/v1/access/registrations` | identity/access | 已有，需改名文案为注册/登录语义 |
| `POST` | `/api/v1/access/login-challenges` | identity | 新增，邮箱 OTP/magic link |
| `POST` | `/api/v1/access/login-challenges/verify` | identity | 新增，验证并绑定设备 |
| `GET` | `/api/v1/users/:user_id/access/snapshot` | access | 已有，需生产签名和错误 code |
| `POST` | `/api/v1/commerce/checkout-sessions` | commerce | 已有，需补 checkout policy reason |
| `POST` | `/api/v1/commerce/subscriptions/cancel` | commerce | 已接 provider subscription control |
| `POST` | `/api/v1/commerce/subscriptions/resume` | commerce | 已接 provider subscription control |
| `POST` | `/api/v1/cloud-storage/sync-sessions` | cloud_storage | 已有，待 provider ADR 后启用 |
| `POST` | `/api/v1/cloud-storage/manifests` | cloud_storage | 已有，需 sync session 校验 |
| `GET` | `/api/v1/users/:user_id/cloud-storage/usage` | cloud_storage | 已有 |
| `GET` | `/api/v1/users/:user_id/cloud-storage/projects` | cloud_storage | 新增 |
| `GET` | `/api/v1/cloud-storage/projects/:id/manifests/latest` | cloud_storage | 新增 |

### 6.2 Provider / Webhook API

| Method | Path | 说明 |
|---|---|---|
| `POST` | `/api/v1/webhooks/:provider` | provider-agnostic webhook inbox，Creem/mock/future provider 共用 |
| `GET` | `/checkout/:out_trade_no` | dev-only mock hosted checkout |
| `POST` | `/checkout/:out_trade_no/complete` | dev-only simulate paid |

### 6.3 Admin API

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/api/v1/admin/users` | 用户列表，默认脱敏 |
| `GET` | `/api/v1/admin/users/:id` | 用户详情，权限更高 |
| `GET` | `/api/v1/admin/users/:id/devices` | 设备列表 |
| `POST` | `/api/v1/admin/devices/:id/revoke` | 撤销设备 |
| `GET` | `/api/v1/admin/users/:id/access` | trial/grants/subscription/orders/payment-events/risk/cloud summary，默认脱敏 |
| `POST` | `/api/v1/admin/grants` | 人工 grant |
| `POST` | `/api/v1/admin/grants/:id/revoke` | 人工撤销 grant |
| `GET` | `/api/v1/admin/orders` | 订单列表，含 payment-event / fulfillment / risk 摘要，默认脱敏 |
| `GET` | `/api/v1/admin/payment-events` | webhook inbox |
| `POST` | `/api/v1/admin/payment-events/:id/reprocess` | 重放处理 |
| `GET` | `/api/v1/admin/payment-risk-flags` | 风险列表 |
| `POST` | `/api/v1/admin/payment-risk-flags/:id/resolve` | 风险解除 |
| `GET` | `/api/v1/admin/cloud-storage/usage` | 云存储总览 |
| `POST` | `/api/v1/admin/test/scenarios/reset` | dev/test-only，重置测试账号或场景 |

## 7. 分阶段实施计划

### WCP-0：架构基线和边界收敛（P0）

进展（2026-06-18）：已开始。当前增量把进程入口缩减为启动/优雅关闭，把 config、DB、migration、repository、provider、service 和路由组装迁入 `internal/app/bootstrap`；新增 module registrar 与 import guard 测试来固定边界。

目标：先把模块边界、依赖方向、启动组装、错误模型固化，避免后续继续在 `cmd/server/main.go` 和 handler 中打补丁。

任务：

- 定义 `internal/app/bootstrap`：封装 config、db、migration、repository、provider registry、module registration。
- 为 identity/access/commerce/cloud/admin 定义 `Module` registrar：`RegisterRoutes`, `RegisterAdminRoutes`, `Services`。
- 建立统一错误模型：`code`, `message`, `details`, `retryable`，handler 只做 error mapping。
- 建立架构约束测试：禁止 service 反向依赖 handler；禁止 access 依赖 payment；禁止 cloud 依赖 commerce。
- 整理 README：声明 `walnut-billing` 是 commercial control plane，不是内容存储服务。

验收标准：

- 无业务行为变化，现有 mock checkout、snapshot、admin、cloud tests 全部通过。
- `cmd/server/main.go` 只保留启动入口，DI 细节迁移到 bootstrap/module registrars。
- 新增模块边界文档和 import guard 测试。
- 新功能 PR 必须说明归属模块和依赖方向。

建议验证命令：

```bash
go test ./internal/service ./internal/api/handler ./internal/repository/gorm_repo ./internal/config ./internal/payment
```

### WCP-1：Creem test mode 与真实 checkout 闭环（P0）

WCP-1 进展（2026-06-18）：第一切片已开始。Creem adapter contract 已增加 sandbox/prod endpoint/key 防混用、checkout-visible SKU product map 校验，以及本地 webhook fixture runner；真实 dashboard 支付仍需后续凭证环境验证。

目标：在 mock provider 保持确定性的同时，能用 Creem sandbox 跑通真实 hosted checkout + webhook + fulfillment。

任务：

- 完成 Creem product map 校验：SKU 缺失、sandbox/prod 混用、无 webhook secret 时启动失败或禁用 provider。
- 完成 Creem webhook fixtures：paid、refund、dispute、cancel、renewal_paid、renewal_failed、subscription_expired。
- `PaymentProviderAdapter` 拆分可选能力：`CheckoutProvider`, `WebhookVerifier`, `SubscriptionControlProvider`。
- Creem event mapper 只输出 Walnut normalized events，不把 Creem 字段传播到 access 层。
- 增强 `docs/LOCAL_COMMERCE_TEST_ENV.md`：Creem sandbox 配置、ngrok/cloudflared webhook、重复测试步骤。
- 新增 `scripts/verify_creem_sandbox_contract.sh` 或 Go integration test fixture runner。

验收标准：

- mock profile 下 1 条命令可重复跑通：注册 -> checkout -> simulate paid -> snapshot Pro。
- Creem sandbox 下能创建真实 checkout URL，支付后 webhook 进入 `PaymentEventInbox`。
- Creem webhook 重放不重复履约。
- Creem refund/dispute 不直接删除本地基础功能，只写 risk/revoke policy 所需事实。
- Provider-specific product id 不出现在 Walnut App response、snapshot 或 feature gate 中。

### WCP-2：账号恢复、设备生命周期和试用风控（P0）

WCP-2 进展（2026-06-18）：第一切片已完成。新增 `AccessLoginChallenge` 邮箱登录/恢复挑战实体、repository port、GORM adapter、service port/strategy、HTTP handler 与 identity/access module routes。OTP 只保存 HMAC hash，challenge 支持 TTL、最大错误次数、幂等创建、一次性原子消费；验证成功后复用 `AccessSessionService.RegisterOrRestore`，避免重复实现 user/device/trial/snapshot 状态机。dev delivery 会返回 `dev_token` 便于本地测试；prod 下 dev delivery 被禁用，后续接入真实 email provider。新增 `scripts/verify_access_login_challenge_contract.sh` 固化 service/handler/repository/config 合同。

WCP-2 进展（2026-06-18）：第二切片已完成。新增 `AccessDeviceAdminService` 作为设备生命周期 admin facade，`POST /api/v1/admin/devices/:id/revoke` 通过 `admin.access_accounts.write` 权限撤销设备并写 `access.device.revoke` audit；`UserDevice` 增加 `revoked_at/revoked_by/revoke_reason`。被撤销设备再次邮箱登录/恢复或刷新 signed access snapshot 时返回稳定错误 `access_device_revoked`，不会被误判为普通设备数量超限。admin access-account projection 增加脱敏 device records，便于运营定位可撤销设备而不暴露 raw device id。新增 `scripts/verify_access_device_lifecycle_contract.sh` 固化设备撤销合同。

WCP-2 进展（2026-06-19）：第三切片已完成。登录 challenge 增加持久化滥用控制策略：按 normalized email 与 hashed client IP 统计创建频率，超过配置窗口返回稳定错误 `login_challenge_rate_limited`；challenge 仅保存 IP/User-Agent hash，不保存原始网络标识。错误验证码或设备不匹配会记录 failure reason，达到最大尝试次数后写隐私安全的 `access.login_challenge.abuse` audit。相关阈值通过 `ACCESS_LOGIN_CHALLENGE_RATE_LIMIT_WINDOW_SECONDS`、`ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_EMAIL`、`ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_IP` 配置，合同测试已纳入 `scripts/verify_access_login_challenge_contract.sh`。

WCP-2 进展（2026-06-19）：第四切片已完成。新增 service 层 `AccessDeviceCapacity` value object，`AccessSessionService.RegisterOrRestore` 在绑定/恢复设备后统一返回 `device_capacity.active_device_count`、`max_devices`、`remaining_device_slots`；`AccessSnapshotIssuer` 在 signed `access_snapshot.device` 中嵌入同一容量投影。容量计算只统计 active devices，被 admin revoke 的设备继续返回稳定错误 `access_device_revoked` 且不占用剩余槽位。handler 不计算业务字段，仍只转发 service projection；相关 service/handler/snapshot 合同已纳入设备生命周期验证。

当前依赖流：

```text
POST /api/v1/access/login-challenges
  -> AccessLoginChallengeHandler
    -> AccessLoginChallengeService
      -> AccessLoginChallengeRepository + TokenGenerator + TokenHasher + ChallengeDelivery + ChallengePolicy

POST /api/v1/access/login-challenges/verify
  -> atomic ConsumePending(challenge)
  -> AccessSessionService.RegisterOrRestore
  -> User/UserDevice/AccessDeviceCapacity/TrialGrant/EntitlementSnapshot/AccessSnapshotV2
```

目标：解决“换设备、重装、删除本地数据后授权如何恢复”的成熟闭环。

任务：

- 新增 `AccessLoginChallenge`，支持 OTP/magic link 的挑战、验证、消费。
- 试用发放改为按 normalized email + grant type 幂等，不因清本地数据重复获得试用。
- 设备绑定策略产品化：默认 2 台，可配置；支持管理员撤销；后续支持用户自助 revoke。
- 恢复成功后返回 signed snapshot，同时返回设备状态和剩余可绑定设备数。
- 增加 rate limit 和异常尝试 audit。

验收标准：

- 同一邮箱在新设备登录能恢复 lifetime/monthly/trial 状态。
- 超过设备限制返回稳定错误 code，UI 可引导用户去“管理设备/联系客服”。
- 删除本地 DB 后不能重复领取试用；但可恢复已购买授权。
- 被 admin revoke 的设备刷新 snapshot 失败或降级 Basic。
- OTP 不以明文形式落库，过期和已消费 challenge 不能复用。

### WCP-3：订阅状态投影和按钮状态闭环（P0）

WCP-3 进展（2026-06-19）：第一切片已完成。新增 service 层 `SoftwareSubscriptionProjector` 作为等价 read model，从 Walnut 自有 `EntitlementGrant` 与 `SubscriptionCancellation` facts 投影 `active`、`cancel_at_period_end`、lifetime 等互斥状态。`SoftwareAccessPlanCheckoutPolicy` 改为依赖投影接口，checkout 被阻断时统一返回 roadmap 约定的机器可读 reason：`already_lifetime`、`subscription_active`、`cancel_at_period_end`、`payment_risk_hold`；handler 映射 `checkout_blocked_by_subscription_state` 且不创建 order/provider session。`AccessSnapshotIssuer` 复用同一投影写入 `license.subscription_status`、`current_period_ends_at`、`cancel_at_period_end`，cancel/resume API response 也返回 projection。该切片未引入 provider-specific 逻辑，Creem 字段仍停留在 payment adapter/order metadata 边界。

WCP-3 进展（2026-06-19）：第二切片已完成。支付层新增可选 `SubscriptionControlProvider` port，`PaymentService` 暴露 provider-neutral cancel/resume facade；mock provider 与 Creem adapter 实现同一接口。Creem test mode 按官方文档使用 `POST /v1/subscriptions/{id}/cancel` 与 `POST /v1/subscriptions/{id}/resume`，test/prod 只由 `PAYMENT_CREEM_SANDBOX`、base URL、key、product map 和 webhook secret 配置切换。`SubscriptionCancellationService` 改为 provider-first 编排：先根据 payment event/order metadata 解析 provider subscription id 并调用 provider cancel/resume，provider 成功后才写 Walnut `SubscriptionCancellation` fact、order metadata 与 projection；provider 失败不写本地取消/恢复事实，可用同一 idempotency key 重试。HTTP handler 返回稳定错误 code：`subscription_control_unavailable`、`subscription_control_failed`、`subscription_not_found`。新增 `scripts/verify_subscription_control_contract.sh` 固化 payment/service/handler/architecture 合同。

目标：服务端提供清晰、互斥、可解释的软件授权状态，让客户端按钮不靠猜。

任务：

- 新增 `SoftwareSubscription` projection 或等价 read model。
- paid / renewal / cancel / resume / expired events 更新 subscription projection。
- Access snapshot 中投影：`license.state`, `plan`, `subscription_status`, `current_period_ends_at`, `cancel_at_period_end`。
- Checkout policy 返回机器可读 reason：`already_lifetime`, `subscription_active`, `cancel_at_period_end`, `payment_risk_hold`。
- cancel/resume 接 provider 可选接口：mock 本地模拟，Creem 调真实 sandbox API。

验收标准：

- 月付 active：客户端显示“退订”，不显示“升级月付”。
- 月付 cancel_at_period_end：客户端显示“恢复月付”和“服务可用至 X”。
- 终身 active：客户端不显示“购买终身版”，也不显示“升级月付”。
- 月付周期计算以 provider period end 或 billing policy 为准，时间展示不少于一个月且时区明确。
- 退订后当前周期内 Pro 权益仍有效，到期后自动降级 Basic。
- 恢复月付后取消标记清除，snapshot 同步更新。

### WCP-4：Admin Console MVP（P1）

WCP-4 进展（2026-06-19）：第一切片已完成。新增 `AdminUserAccessSummaryService` 作为管理端只读 facade，依赖 `AdminUserAccessSummaryReadRepository`、`SoftwareSubscriptionProjector` 与 cloud quota policy，统一投影单个用户的 device capacity、trial、grants、subscription、recent orders、payment events、risk counters 与 cloud quota metadata。HTTP route `GET /api/v1/admin/users/:user_id/access` 由 `admin.users.read` 单独授权，handler 只做 transport mapping；support 账号可被授予 `admin.access_accounts.read` + `admin.users.read` 做脱敏排障，写操作仍需 ops/admin 权限。响应只返回 raw provider payload 的 `payload_hash` 和 provider-neutral 摘要，禁止泄露 raw email、raw device id、checkout URL、provider subscription id、provider event id 或 webhook raw payload。新增 `scripts/verify_admin_user_access_summary_contract.sh` 固化 read model、privacy projection、handler error code、权限边界和架构边界。

WCP-4 进展（2026-06-19）：第二切片已完成。新增 `AdminOrderService` + `AdminOrderReadRepository`，提供 `GET /api/v1/admin/orders` 只读订单排障列表，由 `admin.orders.read` 单独授权。该 read model 只使用 Walnut-owned `out_trade_no`、`user_id`、`sku_code`、状态和金额等稳定字段，并聚合 payment-event count/latest `payload_hash`、fulfillment count/failure count、open risk count，帮助运营从订单视角定位“付款成功未解锁 / webhook 失败 / risk hold”。响应只暴露 provider presence flags（checkout session、provider customer、provider subscription metadata），不返回 checkout URL、provider customer id、provider subscription id、provider event id、idempotency key 或 raw webhook payload。新增 `scripts/verify_admin_order_contract.sh` 固化 read model、handler error code、权限边界和架构边界。

目标：让管理员可以在测试和生产中独立排查用户、授权、支付和风险问题。

任务：

- 后端补齐 admin read APIs：users、devices、access summary、orders、subscriptions、cloud usage。
- dashboard 从静态工具页演进为分区页面；仍可先保持轻量 HTML/JS，不引入复杂前端框架。
- scoped principals 权限矩阵落地：support、ops、finance、admin。
- 所有管理动作写 audit：principal、target、reason、before/after summary。
- 增加 dev/test-only scenario reset：只能在 `SERVER_ENV != prod` 且 `admin.test.write` 时启用。

验收标准：

- 管理员能通过邮箱指纹/脱敏搜索定位测试账号。
- support-key 不能执行写操作；ops-key 能 grant/revoke/reprocess/resolve。
- Dashboard 可显示 checkout 成功率、webhook 失败数、risk flags、active subscription、cloud usage。
- 操作 audit 可追踪到具体 principal。
- prod 环境未配置 admin auth 时服务拒绝启动。

### WCP-5：云存储 ADR 与控制面完善（P1）

目标：在云厂商未确定前不写假实现；先把控制面和 contract tests 准备好。

任务：

- 编写 ADR：评估 OSS、S3/R2、MinIO、managed storage，明确第一实现。
- 完善 `ObjectStorageProvider` contract：upload target、download target、delete、head object、lifecycle tags。
- 新增 `CloudSyncSession`，保证 upload session 与 manifest commit 绑定。
- 增加 restore APIs：project list、latest manifest、object download target。
- Quota policy 从固定 MB 演进为基于 access plan：trial/monthly/lifetime 可配置。

验收标准：

- provider 未配置时 cloud API 明确失败，不影响软件授权其他闭环。
- provider 配置后，App 可直传对象并 commit manifest。
- quota 计算基于 latest active objects，不重复计算被替换对象。
- restore 能在新设备上列出项目和最新 manifest。
- 云存储 admin 页面只展示 metadata，不展示正文或原文件内容。

### WCP-6：生产安全和可运维性（P1）

目标：达到小规模付费上线的生产门槛。

任务：

- prod config validation：admin principals、snapshot signer、Creem key/webhook secret、DB DSN、rate limit、CORS/redirect allowlist。
- 数据迁移策略：替代裸 `AutoMigrate` 的版本化 migration，保留 rollback/runbook。
- backup/restore runbook：SQLite 初期备份，后续 Postgres migration path。
- webhook retry / dead letter / admin reprocess runbook。
- 安全审计：secret redaction、raw payload retention、PII retention、admin action review。
- 监控告警：checkout failure spike、webhook failed、fulfillment failed、snapshot signing errors、quota overage。

验收标准：

- prod 环境缺关键安全配置直接启动失败。
- 所有外部 webhook 有签名校验、payload hash、replay protection。
- 每个 payment event 最终状态可解释：processed / ignored / failed / review_required。
- 支持从备份恢复到指定时间点或最近一致快照。
- Runbook 可让非开发人员完成常见问题排查：付款成功未解锁、重复扣费、退订、换设备、风险解除。

## 8. 测试策略

### 8.1 测试分层

| 层级 | 覆盖内容 | 示例 |
|---|---|---|
| Unit tests | policy、mapper、signer、quota、state machine | checkout policy、subscription projection、Creem mapper |
| Repository tests | GORM repo、唯一键、事务、幂等 | payment event unique, fulfillment idempotency |
| Service tests | 模块业务闭环 | access restore、cancel/resume、cloud commit |
| Handler tests | API DTO、错误 code、权限 | admin RBAC、checkout errors |
| Contract tests | provider adapter contract | mock/Creem webhook fixtures, object storage provider contract |
| E2E scripts | 可重复商业流程 | deterministic mock checkout, Creem sandbox runbook |

### 8.2 必须保留的验证命令

```bash
# walnut-billing core
go test ./internal/service ./internal/api/handler ./internal/repository/gorm_repo ./internal/config ./internal/payment

# deterministic local commerce
scripts/reset_deterministic_billing.sh
scripts/run_deterministic_billing.sh

# with Walnut Core, from sagemate-core
python scripts/verify_billing_checkout_e2e.py \
  --core-url http://127.0.0.1:8000 \
  --billing-url http://127.0.0.1:8082 \
  --admin-key local-admin-key \
  --email checkout-e2e+001@example.com \
  --sku pro_own_ai_monthly
```

### 8.3 全局 Definition of Done

每个里程碑必须满足：

- 有模块边界说明，新增代码归属明确。
- 有单元测试或 handler/service 测试覆盖核心状态机。
- 有至少一个可执行 runbook 或脚本覆盖主路径。
- 新增 API 有稳定错误 code，不要求客户端解析自然语言文案。
- admin 写操作有权限控制和 audit。
- 不引入 provider/SKU 到 App feature gate。
- 不引入未决策 provider 的假实现。
- `go test` 通过，且 deterministic mock profile 可运行。

## 9. 需求决策清单

| 决策 | 当前建议 | 阻塞模块 |
|---|---|---|
| 云存储 provider | 先 ADR，provider 未定前只保留 interface | WCP-5 provider implementation |
| 邮件 provider | dev 使用 console/mock；prod 选择 SES/Resend/SendGrid/自建 SMTP | WCP-2 OTP/magic link |
| 生产 DB | MVP 可 SQLite + backup；付费规模上来迁 Postgres | WCP-6 |
| Creem 产品 ID | test/prod product map 分开配置，不进代码 | WCP-1 |
| Lifetime 云存储策略 | 固定 quota + fair-use，不能承诺无限 | WCP-5 |
| 月付升级终身时月付如何处理 | 建议提示用户先退订，或购买终身后自动 cancel at period end；需产品确认 | WCP-3 |
| 管理员明文邮箱权限 | 默认不展示；如需详情查看必须高权限 + audit | WCP-4 |

## 10. 推荐推进顺序

P0 必须先做：

1. WCP-0：拆启动组装、固化模块边界和错误模型。
2. WCP-1：Creem sandbox + mock 两套闭环都可重复测试。
3. WCP-2：邮箱注册/登录/恢复 + 设备生命周期。
4. WCP-3：订阅状态投影 + 月付/终身互斥 + 按钮状态服务端闭环。

P1 随后推进：

5. WCP-4：Admin Console MVP。
6. WCP-5：云存储 ADR + 控制面完善。
7. WCP-6：生产安全、迁移、备份、监控、告警。

判断阶段性闭环的标准：

```text
新用户输入邮箱
  -> 获得 trial snapshot
  -> 点击升级月付/终身
  -> mock 或 Creem test checkout 成功
  -> webhook 幂等履约
  -> App 刷新后只解锁 editorial.studio + cloud.storage
  -> 月付可退订/恢复，终身与月付互斥
  -> 换设备/清本地数据后可通过邮箱恢复
  -> 管理后台可脱敏排查全链路
```

达到以上标准后，`walnut-billing` 才算完成 BYOK-first 商业授权的生产级闭环；云存储 provider 的真实落地可以作为下一阶段服务化能力，但控制面接口和权限边界必须先稳定。
