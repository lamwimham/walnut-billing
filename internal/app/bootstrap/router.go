package bootstrap

import (
	"fmt"
	"log/slog"

	"walnut-billing/internal/api/handler"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/config"
	"walnut-billing/internal/metrics"

	"github.com/gin-gonic/gin"
)

type applicationHandlers struct {
	Auth                 *handler.AuthHandler
	Order                *handler.OrderHandler
	OrderQuery           *handler.OrderQueryHandler
	Renewal              *handler.RenewalHandler
	Admin                *handler.AdminHandler
	PaymentConfig        *handler.PaymentConfigHandler
	Health               *handler.HealthHandler
	Dashboard            *handler.DashboardHandler
	Entitlement          *handler.EntitlementHandler
	AccessSession        *handler.AccessSessionHandler
	AccessLoginChallenge *handler.AccessLoginChallengeHandler
	AccessAdmin          *handler.AccessAdminHandler
	AccessSnapshot       *handler.AccessSnapshotHandler
	Credit               *handler.CreditHandler
	Checkout             *handler.CheckoutHandler
	AdminOrder           *handler.AdminOrderHandler
	AdminSubscription    *handler.AdminSubscriptionHandler
	AdminCloudStorage    *handler.AdminCloudStorageHandler
	Subscription         *handler.SubscriptionHandler
	PaymentEvent         *handler.PaymentEventHandler
	MockCheckout         *handler.MockCheckoutHandler
	PaymentRisk          *handler.PaymentRiskHandler
	Fulfillment          *handler.FulfillmentHandler
	CloudStorage         *handler.CloudStorageHandler
	LicenseDeactivation  *handler.DeactivateHandler
}

type routerDependencies struct {
	Config   *config.Config
	Logger   *slog.Logger
	Handlers applicationHandlers
}

type moduleRegistrar interface {
	RegisterRoutes(routes moduleRoutes)
}

type moduleRoutes struct {
	Public       *gin.RouterGroup
	Admin        *gin.RouterGroup
	RequireAdmin func(permission string) gin.HandlerFunc
}

func buildRouter(deps routerDependencies) (*gin.Engine, error) {
	cfg := deps.Config
	if cfg.Server.Env == config.ProductionEnv {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.Recovery(deps.Logger))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(deps.Logger))
	r.Use(middleware.SecurityHeaders(middleware.SecurityHeadersConfig{
		Enabled:           cfg.HTTP.SecurityHeaders.Enabled,
		HSTSMaxAgeSeconds: cfg.HTTP.SecurityHeaders.HSTSMaxAgeSeconds,
	}))
	if len(cfg.HTTP.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(middleware.CORSConfig{AllowedOrigins: cfg.HTTP.CORSAllowedOrigins}))
	}
	r.Use(metrics.Middleware())
	if cfg.Server.Env == config.ProductionEnv {
		r.SetTrustedProxies(nil)
	}

	api := r.Group("/api/v1")
	if cfg.RateLimit.Enabled {
		limiter := middleware.NewIPRateLimiter(cfg.RateLimit.MaxTokens, cfg.RateLimit.RefillRate)
		api.Use(middleware.RateLimit(limiter))
		deps.Logger.Info("Rate limiting enabled", "max_tokens", cfg.RateLimit.MaxTokens, "refill_rate", cfg.RateLimit.RefillRate)
	}

	admin := r.Group("/api/v1/admin")
	adminAuthEnabled, err := configureAdminAuth(admin, cfg.Admin, cfg.Server.Env, deps.Logger)
	if err != nil {
		return nil, err
	}
	requireAdmin := func(permission string) gin.HandlerFunc { return permissionGate(adminAuthEnabled, permission) }

	routes := moduleRoutes{Public: api, Admin: admin, RequireAdmin: requireAdmin}
	for _, registrar := range []moduleRegistrar{
		identityAccessModule{handlers: deps.Handlers},
		cloudStorageModule{handlers: deps.Handlers},
		commerceModule{handlers: deps.Handlers},
		legacyLicenseModule{handlers: deps.Handlers},
		adminModule{handlers: deps.Handlers},
	} {
		registrar.RegisterRoutes(routes)
	}

	registerInfrastructureRoutes(r, routes, deps.Handlers, cfg.Server.Env)
	return r, nil
}

func configureAdminAuth(admin *gin.RouterGroup, cfg config.AdminConfig, serverEnv string, logger *slog.Logger) (bool, error) {
	adminPrincipals := buildAdminPrincipals(cfg)
	if len(adminPrincipals) > 0 {
		admin.Use(middleware.APIKeyAuthPrincipals(adminPrincipals))
		logger.Info("Admin API authentication enabled", "principals_count", len(adminPrincipals))
		return true, nil
	}
	if serverEnv == config.ProductionEnv {
		err := fmt.Errorf("admin API authentication is required in production (set ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON)")
		logger.Error("Admin API authentication is required in production", "error", err)
		return false, err
	}
	logger.Warn("Admin API authentication disabled - no admin principals configured (set ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON)")
	return false, nil
}

// identityAccessModule owns registration, access snapshots, grants, and credit ledger routes.
type identityAccessModule struct{ handlers applicationHandlers }

func (m identityAccessModule) RegisterRoutes(routes moduleRoutes) {
	h := m.handlers
	routes.Public.POST("/access/registrations", h.AccessSession.RegisterOrRestore)
	routes.Public.POST("/access/login-challenges", h.AccessLoginChallenge.Create)
	routes.Public.POST("/access/login-challenges/verify", h.AccessLoginChallenge.Verify)
	routes.Public.GET("/users/:user_id/access/snapshot", h.AccessSnapshot.GetSnapshot)
	routes.Public.POST("/registrations", h.Entitlement.SubmitRegistration)
	routes.Public.GET("/users/:user_id/entitlements/snapshot", h.Entitlement.GetUserEntitlementSnapshot)
	routes.Public.GET("/users/:user_id/credits/account", h.Credit.GetAccount)
	routes.Public.POST("/credits/reservations", h.Credit.Reserve)
	routes.Public.POST("/credits/reservations/:id/commit", h.Credit.Commit)
	routes.Public.POST("/credits/reservations/:id/release", h.Credit.Release)

	routes.Admin.GET("/access-accounts", routes.RequireAdmin(middleware.PermissionAccessAccountsRead), h.AccessAdmin.ListAccounts)
	routes.Admin.GET("/users/:user_id/access", routes.RequireAdmin(middleware.PermissionUsersRead), h.AccessAdmin.GetUserAccessSummary)
	routes.Admin.POST("/devices/:id/revoke", routes.RequireAdmin(middleware.PermissionAccessAccountsWrite), h.AccessAdmin.RevokeDevice)
	routes.Admin.GET("/registrations", routes.RequireAdmin(middleware.PermissionRegistrationsRead), h.Entitlement.ListRegistrations)
	routes.Admin.POST("/registrations/:id/review", routes.RequireAdmin(middleware.PermissionRegistrationsWrite), h.Entitlement.ReviewRegistration)
	routes.Admin.GET("/grants", routes.RequireAdmin(middleware.PermissionEntitlementGrantsRead), h.Entitlement.ListGrants)
	routes.Admin.POST("/grants", routes.RequireAdmin(middleware.PermissionEntitlementGrantsWrite), h.Entitlement.CreateGrant)
	routes.Admin.POST("/credits/grants", routes.RequireAdmin(middleware.PermissionCreditsWrite), h.Credit.Grant)
	routes.Admin.POST("/credits/buckets/expire", routes.RequireAdmin(middleware.PermissionCreditsWrite), h.Credit.ExpireBuckets)
	routes.Admin.GET("/users/:user_id/credits/transactions", routes.RequireAdmin(middleware.PermissionCreditsRead), h.Credit.ListTransactions)
	routes.Admin.GET("/users/:user_id/credits/usage-records", routes.RequireAdmin(middleware.PermissionCreditsRead), h.Credit.ListUsageRecords)
}

// cloudStorageModule owns control-plane metadata only; object bytes stay with storage providers.
type cloudStorageModule struct{ handlers applicationHandlers }

func (m cloudStorageModule) RegisterRoutes(routes moduleRoutes) {
	h := m.handlers
	routes.Public.POST("/cloud-storage/sync-sessions", h.CloudStorage.AuthorizeSync)
	routes.Public.POST("/cloud-storage/manifests", h.CloudStorage.CommitManifest)
	routes.Public.GET("/users/:user_id/cloud-storage/usage", h.CloudStorage.Usage)
	routes.Public.GET("/users/:user_id/cloud-storage/projects", h.CloudStorage.ListProjects)
	routes.Public.GET("/cloud-storage/projects/:project_id/manifests/latest", h.CloudStorage.LatestManifest)
	routes.Public.POST("/cloud-storage/download-targets", h.CloudStorage.BuildDownloadTarget)

	routes.Admin.GET("/cloud-storage/usage", routes.RequireAdmin(middleware.PermissionCloudStorageRead), h.AdminCloudStorage.Usage)
	routes.Admin.GET("/users/:user_id/cloud-storage/projects", routes.RequireAdmin(middleware.PermissionCloudStorageRead), h.AdminCloudStorage.ListUserProjects)
}

// commerceModule owns checkout, provider webhook inbox, fulfillment diagnostics, and payment risk routes.
type commerceModule struct{ handlers applicationHandlers }

func (m commerceModule) RegisterRoutes(routes moduleRoutes) {
	h := m.handlers
	routes.Public.POST("/commerce/checkout-sessions", h.Checkout.CreateCheckoutSession)
	routes.Public.POST("/commerce/subscriptions/cancel", h.Subscription.Cancel)
	routes.Public.POST("/commerce/subscriptions/resume", h.Subscription.Resume)
	routes.Public.POST("/webhooks/:provider", h.PaymentEvent.ReceiveWebhook)

	routes.Admin.GET("/payment/providers", routes.RequireAdmin(middleware.PermissionPaymentRead), h.PaymentConfig.GetProviderStatus)
	routes.Admin.PUT("/payment/wechat", routes.RequireAdmin(middleware.PermissionPaymentWrite), h.PaymentConfig.UpdateWechatConfig)
	routes.Admin.PUT("/payment/alipay", routes.RequireAdmin(middleware.PermissionPaymentWrite), h.PaymentConfig.UpdateAlipayConfig)
	routes.Admin.PUT("/payment/creem", routes.RequireAdmin(middleware.PermissionPaymentWrite), h.PaymentConfig.UpdateCreemConfig)
	routes.Admin.POST("/payment/:provider/mock", routes.RequireAdmin(middleware.PermissionPaymentWrite), h.PaymentConfig.SwitchToMock)
	routes.Admin.POST("/payment/import", routes.RequireAdmin(middleware.PermissionPaymentWrite), h.PaymentConfig.ImportProviders)
	routes.Admin.GET("/orders", routes.RequireAdmin(middleware.PermissionOrdersRead), h.AdminOrder.ListOrders)
	routes.Admin.GET("/subscriptions", routes.RequireAdmin(middleware.PermissionSubscriptionsRead), h.AdminSubscription.ListSubscriptions)
	routes.Admin.GET("/payment-events", routes.RequireAdmin(middleware.PermissionPaymentEventsRead), h.PaymentEvent.ListEvents)
	routes.Admin.GET("/payment-events/:id", routes.RequireAdmin(middleware.PermissionPaymentEventsRead), h.PaymentEvent.GetEvent)
	routes.Admin.POST("/payment-events/:id/reprocess", routes.RequireAdmin(middleware.PermissionPaymentEventsWrite), h.PaymentEvent.ReprocessEvent)
	routes.Admin.GET("/payment-risk-flags", routes.RequireAdmin(middleware.PermissionPaymentRiskRead), h.PaymentRisk.ListFlags)
	routes.Admin.GET("/payment-risk-flags/:id", routes.RequireAdmin(middleware.PermissionPaymentRiskRead), h.PaymentRisk.GetFlag)
	routes.Admin.POST("/payment-risk-flags/:id/resolve", routes.RequireAdmin(middleware.PermissionPaymentRiskWrite), h.PaymentRisk.ResolveFlag)
	routes.Admin.GET("/fulfillments", routes.RequireAdmin(middleware.PermissionFulfillmentsRead), h.Fulfillment.ListExecutions)
}

// legacyLicenseModule keeps existing license/order APIs stable while commerce migrates to checkout sessions.
type legacyLicenseModule struct{ handlers applicationHandlers }

func (m legacyLicenseModule) RegisterRoutes(routes moduleRoutes) {
	h := m.handlers
	routes.Public.POST("/verify", h.Auth.Verify)
	routes.Public.POST("/activate", h.Auth.Activate)
	routes.Public.POST("/orders", h.Order.CreateOrder)
	routes.Public.POST("/orders/pay", h.Order.GetPaymentURL)
	routes.Public.GET("/orders/:out_trade_no", h.OrderQuery.GetOrder)
	routes.Public.POST("/callbacks/:provider", h.Order.PaymentCallback)
	routes.Public.POST("/deactivate", h.LicenseDeactivation.Deactivate)
	routes.Public.POST("/orders/renew", h.Renewal.CreateRenewalOrder)
	routes.Public.POST("/orders/renew/pay", h.Renewal.RenewAndPay)
	routes.Public.GET("/licenses/:key/status", h.Admin.LicenseStatus)

	routes.Admin.GET("/licenses", routes.RequireAdmin(middleware.PermissionLicensesRead), h.Admin.ListLicenses)
	routes.Admin.GET("/licenses/:key", routes.RequireAdmin(middleware.PermissionLicensesRead), h.Admin.GetLicense)
	routes.Admin.GET("/stats", routes.RequireAdmin(middleware.PermissionLicensesRead), h.Admin.Stats)
	routes.Admin.POST("/licenses/check-expiry", routes.RequireAdmin(middleware.PermissionLicensesWrite), h.Admin.CheckExpiry)
	routes.Admin.GET("/licenses/expiring", routes.RequireAdmin(middleware.PermissionLicensesRead), h.Admin.ListExpiring)
}

// adminModule owns cross-module operator surfaces that are not specific to one business context.
type adminModule struct{ handlers applicationHandlers }

func (m adminModule) RegisterRoutes(routes moduleRoutes) {
	routes.Admin.GET("/audit", routes.RequireAdmin(middleware.PermissionAuditRead), m.handlers.Admin.GetAuditLogs)
}

func registerInfrastructureRoutes(r *gin.Engine, routes moduleRoutes, h applicationHandlers, serverEnv string) {
	r.GET("/ping", h.Health.Ping)
	r.GET("/health", h.Health.Health)
	r.GET("/metrics", metrics.Handler())
	r.GET("/dashboard", handler.ServeDashboard)
	routes.Admin.GET("/dashboard", routes.RequireAdmin(middleware.PermissionDashboardRead), h.Dashboard.GetDashboard)
	if serverEnv != config.ProductionEnv {
		r.GET("/checkout/:out_trade_no", h.MockCheckout.Show)
		r.POST("/checkout/:out_trade_no/complete", h.MockCheckout.Complete)
	}
}
