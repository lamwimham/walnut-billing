package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"walnut-billing/internal/api/handler"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/config"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/generator"
	"walnut-billing/internal/logger"
	"walnut-billing/internal/metrics"
	"walnut-billing/internal/observability"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
	gorm_repo "walnut-billing/internal/repository/gorm_repo"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func buildAdminPrincipals(cfg config.AdminConfig) []middleware.AdminPrincipal {
	principals := middleware.PrincipalsFromAPIKeys(cfg.APIKeys)
	for _, principal := range cfg.Principals {
		principals = append(principals, middleware.AdminPrincipal{
			Name:        strings.TrimSpace(principal.Name),
			APIKey:      strings.TrimSpace(principal.Key),
			Permissions: principal.Permissions,
		})
	}
	return principals
}

func permissionGate(enabled bool, permission string) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) { c.Next() }
	}
	return middleware.RequirePermission(permission)
}

func buildCheckoutPolicies(
	cfg *config.Config,
	paymentRiskFlagRepo repository.PaymentRiskFlagRepository,
	grantRepo repository.EntitlementGrantRepository,
	subscriptionCancellationRepo repository.SubscriptionCancellationRepository,
) []service.CheckoutPolicy {
	policies := []service.CheckoutPolicy{
		service.NewSoftwareAccessPlanCheckoutPolicy(grantRepo, subscriptionCancellationRepo, nil),
	}
	if cfg != nil && cfg.Checkout.RiskPolicyEnabled {
		riskConfig := service.DefaultCheckoutRiskPolicyConfig()
		riskConfig.BlockSeverities = cfg.Checkout.RiskBlockSeverities
		policies = append(policies, service.NewPaymentRiskCheckoutPolicy(paymentRiskFlagRepo, riskConfig))
	}
	return policies
}

func buildPaymentAdjustmentPolicy(cfg *config.Config) service.PaymentAdjustmentPolicy {
	policyConfig := service.DefaultPaymentAdjustmentPolicyConfig()
	if cfg != nil {
		policyConfig.RefundWindowDays = cfg.Adjustment.RefundWindowDays
		policyConfig.RefundInWindowAction = cfg.Adjustment.RefundInWindowAction
		policyConfig.RefundOutOfWindowAction = cfg.Adjustment.RefundOutOfWindowAction
		policyConfig.LowUsagePolicyEnabled = cfg.Adjustment.LowUsagePolicyEnabled
		policyConfig.LowUsageMaxCreditsUsed = cfg.Adjustment.LowUsageMaxCreditsUsed
		policyConfig.LowUsageAction = cfg.Adjustment.LowUsageAction
		policyConfig.HighUsageAction = cfg.Adjustment.HighUsageAction
		policyConfig.DisputeAction = cfg.Adjustment.DisputeAction
		policyConfig.CancelAction = cfg.Adjustment.CancelAction
	}
	return service.NewConfigurablePaymentAdjustmentPolicy(policyConfig)
}

func buildSubscriptionRenewalPolicy(cfg *config.Config) service.SubscriptionRenewalPolicy {
	policyConfig := service.DefaultSubscriptionRenewalPolicyConfig()
	if cfg != nil {
		policyConfig.GracePeriodDays = cfg.Renewal.GracePeriodDays
		policyConfig.ExpiredAction = cfg.Renewal.ExpiredAction
	}
	return service.NewConfigurableSubscriptionRenewalPolicy(policyConfig)
}

func buildAccessSessionPolicy(cfg *config.Config) service.AccessSessionPolicy {
	policyConfig := service.DefaultAccessSessionPolicyConfig()
	if cfg != nil {
		policyConfig.TrialDurationDays = cfg.Access.TrialDurationDays
		policyConfig.MaxDevices = cfg.Access.MaxDevices
	}
	return service.NewConfigurableAccessSessionPolicy(policyConfig)
}

func buildAccessSnapshotPolicy(cfg *config.Config) service.AccessSnapshotPolicy {
	policyConfig := service.DefaultAccessSnapshotPolicyConfig()
	if cfg != nil {
		policyConfig.TTLSeconds = cfg.Access.SnapshotTTLSeconds
		policyConfig.OfflineGraceSeconds = cfg.Access.SnapshotOfflineGraceSeconds
		policyConfig.MaxDevices = cfg.Access.MaxDevices
		policyConfig.CloudStorageQuotaMB = cfg.Access.CloudStorageQuotaMB
	}
	return service.NewConfigurableAccessSnapshotPolicy(policyConfig)
}

func buildCloudObjectStorageProvider(cfg *config.Config) (service.ObjectStorageProvider, error) {
	providerID := ""
	if cfg != nil {
		providerID = strings.TrimSpace(cfg.CloudStorage.Provider)
	}
	switch providerID {
	case "":
		return service.NewUnconfiguredObjectStorageProvider(), nil
	default:
		return nil, fmt.Errorf("cloud storage provider %q is not implemented yet", providerID)
	}
}

func buildAccessSnapshotSigner(cfg *config.Config) (service.AccessSnapshotSigner, error) {
	algorithm := "HS256"
	keyID := ""
	if cfg != nil {
		algorithm = strings.TrimSpace(cfg.Access.SnapshotSignatureAlgorithm)
		keyID = cfg.Access.SnapshotKeyID
		switch algorithm {
		case "Ed25519", "EdDSA":
			return service.NewEd25519AccessSnapshotSigner(cfg.Access.SnapshotPrivateKey, keyID)
		case "", "HS256":
			if cfg.Server.Env == "prod" {
				return nil, service.ErrInvalidAccessSnapshot
			}
			secret := cfg.Access.SnapshotSecret
			if strings.TrimSpace(secret) == "walnut-dev-access-snapshot-secret" && cfg.Server.Env == "prod" {
				return nil, service.ErrInvalidAccessSnapshot
			}
			return service.NewHMACAccessSnapshotSigner(secret, keyID)
		default:
			return nil, service.ErrInvalidAccessSnapshot
		}
	}
	return service.NewHMACAccessSnapshotSigner("walnut-dev-access-snapshot-secret", keyID)
}

func main() {
	// 0. Init Logger
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	l := logger.Init(cfg.Server.Env)

	l.Info("Starting walnut Billing Server",
		"version", "0.3.0",
		"env", cfg.Server.Env,
		"port", cfg.Server.Port,
	)

	// 1. Init Database
	// Use file: DSN with explicit mode to ensure write access
	dsn := cfg.Database.DSN
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn + "?_journal_mode=WAL&_busy_timeout=5000"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		l.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	l.Info("Database connected", "driver", cfg.Database.Driver)

	// 2. Auto Migrate
	if err := db.AutoMigrate(
		&domain.License{},
		&domain.Order{},
		&domain.Product{},
		&domain.AuditEntry{},
		&domain.User{},
		&domain.RegistrationRequest{},
		&domain.EntitlementGrant{},
		&domain.UserDevice{},
		&domain.TrialGrant{},
		&domain.CreditAccount{},
		&domain.CreditBucket{},
		&domain.CreditReservation{},
		&domain.CreditTransaction{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
		&domain.PaymentRiskFlag{},
		&domain.SubscriptionCancellation{},
		&domain.CloudProject{},
		&domain.CloudManifest{},
		&domain.CloudObject{},
	); err != nil {
		l.Error("Failed to migrate database", "error", err)
		os.Exit(1)
	}

	// 3. Init repositories
	licRepo := &gorm_repo.LicenseRepo{DB: db}
	orderRepo := &gorm_repo.OrderRepo{DB: db}
	productRepo := &gorm_repo.ProductRepo{DB: db}
	auditRepo := &gorm_repo.AuditRepo{DB: db}
	userRepo := &gorm_repo.UserRepo{DB: db}
	registrationRepo := &gorm_repo.RegistrationRepo{DB: db}
	grantRepo := &gorm_repo.EntitlementGrantRepo{DB: db}
	userDeviceRepo := &gorm_repo.UserDeviceRepo{DB: db}
	trialGrantRepo := &gorm_repo.TrialGrantRepo{DB: db}
	accessAccountRepo := &gorm_repo.AccessAccountReadRepo{DB: db}
	creditAccountRepo := &gorm_repo.CreditAccountRepo{DB: db}
	creditBucketRepo := &gorm_repo.CreditBucketRepo{DB: db}
	creditReservationRepo := &gorm_repo.CreditReservationRepo{DB: db}
	creditTransactionRepo := &gorm_repo.CreditTransactionRepo{DB: db}
	paymentEventRepo := &gorm_repo.PaymentEventRepo{DB: db}
	fulfillmentExecutionRepo := &gorm_repo.FulfillmentExecutionRepo{DB: db}
	paymentRiskFlagRepo := &gorm_repo.PaymentRiskFlagRepo{DB: db}
	subscriptionCancellationRepo := &gorm_repo.SubscriptionCancellationRepo{DB: db}
	cloudProjectRepo := &gorm_repo.CloudProjectRepo{DB: db}
	cloudManifestRepo := &gorm_repo.CloudManifestRepo{DB: db}
	cloudObjectRepo := &gorm_repo.CloudObjectRepo{DB: db}

	// 4. Reconcile the commercial catalog into storage. The catalog owns
	// active SKUs, non-checkout plans, and hidden legacy SKU compatibility.
	commerceCatalog := service.DefaultCommerceCatalog()
	productCatalogReconciler := service.NewProductCatalogReconciler(productRepo, commerceCatalog.Products())
	if result, err := productCatalogReconciler.Reconcile(context.Background()); err != nil {
		l.Error("Failed to reconcile product catalog", "error", err)
		os.Exit(1)
	} else {
		l.Info("Product catalog reconciled", "created", result.Created, "updated", result.Updated, "unchanged", result.Unchanged)
	}

	// 5. Init Key Generator Factory
	keyFactory := generator.DefaultFactory()

	// 6. Init Services
	licSvc := service.NewLicenseService(licRepo)
	auditSvc := service.NewAuditService(auditRepo, 100, slog.Default())
	uowFactory := func() repository.UnitOfWork {
		return gorm_repo.NewUnitOfWork(db)
	}
	orderSvc := service.NewOrderService(orderRepo, productRepo, licRepo, keyFactory, uowFactory)
	creditSvc := service.NewCreditServiceWithBuckets(userRepo, creditAccountRepo, creditReservationRepo, creditTransactionRepo, creditBucketRepo, uowFactory)
	entitlementCatalog := commerceCatalog.Entitlements()
	entitlementSvc := service.NewEntitlementServiceWithCredits(userRepo, registrationRepo, grantRepo, creditAccountRepo, entitlementCatalog)
	accessSnapshotSigner, err := buildAccessSnapshotSigner(cfg)
	if err != nil {
		l.Error("Failed to initialize access snapshot signer", "error", err)
		os.Exit(1)
	}
	accessSnapshotIssuer := service.NewAccessSnapshotIssuer(service.AccessSnapshotIssuerDependencies{
		Repositories: service.AccessSnapshotIssuerRepositories{
			Users:             userRepo,
			Devices:           userDeviceRepo,
			TrialGrants:       trialGrantRepo,
			EntitlementGrants: grantRepo,
			CreditAccounts:    creditAccountRepo,
			Orders:            orderRepo,
			Cancellations:     subscriptionCancellationRepo,
		},
		Policy: buildAccessSnapshotPolicy(cfg),
		Signer: accessSnapshotSigner,
	})
	accessAdminSvc := service.NewAccessAdminService(accessAccountRepo)
	accessSessionSvc := service.NewAccessSessionService(service.AccessSessionDependencies{
		Repositories: service.AccessSessionRepositories{
			Users:             userRepo,
			Devices:           userDeviceRepo,
			TrialGrants:       trialGrantRepo,
			EntitlementGrants: grantRepo,
			CreditAccounts:    creditAccountRepo,
		},
		Policy:             buildAccessSessionPolicy(cfg),
		EntitlementCatalog: entitlementCatalog,
		SnapshotIssuer:     accessSnapshotIssuer,
		UnitOfWorkFactory:  uowFactory,
	})
	cloudObjectProvider, err := buildCloudObjectStorageProvider(cfg)
	if err != nil {
		l.Error("Failed to initialize cloud storage provider", "error", err)
		os.Exit(1)
	}
	cloudStorageSvc := service.NewCloudStorageService(service.CloudStorageDependencies{
		Users:             userRepo,
		Grants:            grantRepo,
		Projects:          cloudProjectRepo,
		Manifests:         cloudManifestRepo,
		Objects:           cloudObjectRepo,
		Policy:            service.NewCloudStorageQuotaPolicyFromMB(cfg.Access.CloudStorageQuotaMB),
		Provider:          cloudObjectProvider,
		UnitOfWorkFactory: uowFactory,
	})
	fulfillmentCatalog, err := service.NewFulfillmentCatalogFromJSON(cfg.Fulfillment.RulesJSON, commerceCatalog.FulfillmentRules())
	if err != nil {
		l.Error("Failed to load fulfillment catalog", "error", err)
		os.Exit(1)
	}

	// 7. Init Payment Gateway (Registry + Adapter Pattern)
	notifyURL := "http://localhost:" + cfg.Server.Port + "/api/v1/callbacks"
	webhookURL := "http://localhost:" + cfg.Server.Port + "/api/v1/webhooks"
	registry := payment.NewProviderRegistry()

	// WeChat Pay
	if err := cfg.Payment.WechatConfig().Validate(); err == nil {
		wechatAdapter, err := payment.NewWechatPayV3Adapter(cfg.Payment.WechatConfig())
		if err != nil {
			l.Warn("Failed to init WeChat Pay V3, using mock", "error", err)
			mock := payment.NewWechatPayMockAdapter(cfg.Payment.WechatMchID, notifyURL+"/wechat")
			registry.Register("wechat", mock, payment.ProviderStatus{
				IsMock:    true,
				NotifyURL: notifyURL + "/wechat",
			})
		} else {
			registry.Register("wechat", wechatAdapter, payment.ProviderStatus{
				IsMock:      false,
				SandboxMode: cfg.Payment.WechatSandbox,
				NotifyURL:   notifyURL + "/wechat",
			})
			l.Info("WeChat Pay V3 adapter initialized", "sandbox", cfg.Payment.WechatSandbox)
		}
	} else {
		mock := payment.NewWechatPayMockAdapter(cfg.Payment.WechatMchID, notifyURL+"/wechat")
		registry.Register("wechat", mock, payment.ProviderStatus{
			IsMock:    true,
			NotifyURL: notifyURL + "/wechat",
		})
		l.Info("WeChat Pay mock adapter (no credentials)")
	}

	mockCheckoutBaseURL := strings.TrimSpace(cfg.Payment.MockCheckoutBaseURL)
	if mockCheckoutBaseURL == "" {
		mockCheckoutBaseURL = "http://localhost:" + cfg.Server.Port
	}
	if cfg.Server.Env != "prod" {
		registry.Register("mock", payment.NewCheckoutMockAdapterWithBaseURL(notifyURL+"/mock", mockCheckoutBaseURL), payment.ProviderStatus{
			IsMock:    true,
			NotifyURL: notifyURL + "/mock",
		})
		l.Info("Generic checkout mock adapter initialized", "checkout_base_url", mockCheckoutBaseURL)
	}

	// Creem hosted checkout. Creem stays behind the provider adapter boundary:
	// checkout/webhook mapping live here, while fulfillment owns Walnut grants.
	if creemAdapter, err := payment.NewCreemAdapter(cfg.Payment.CreemConfig()); err == nil {
		registry.Register("creem", creemAdapter, payment.ProviderStatus{
			IsMock:      false,
			SandboxMode: cfg.Payment.CreemSandbox,
			NotifyURL:   webhookURL + "/creem",
		})
		l.Info("Creem checkout adapter initialized", "sandbox", cfg.Payment.CreemSandbox)
	} else if cfg.Payment.CreemAPIKey != "" || cfg.Payment.CreemWebhookSecret != "" || cfg.Payment.CreemProductMapJSON != "" {
		l.Warn("Creem checkout adapter not initialized", "error", err)
	}

	// Alipay
	if err := cfg.Payment.AlipayConfig().Validate(); err == nil {
		alipayAdapter, err := payment.NewAlipayV2Adapter(cfg.Payment.AlipayConfig())
		if err != nil {
			l.Warn("Failed to init Alipay, using mock", "error", err)
			mock := payment.NewAlipayMockAdapter(cfg.Payment.AlipayAppID, notifyURL+"/alipay")
			registry.Register("alipay", mock, payment.ProviderStatus{
				IsMock:    true,
				NotifyURL: notifyURL + "/alipay",
			})
		} else {
			registry.Register("alipay", alipayAdapter, payment.ProviderStatus{
				IsMock:      false,
				SandboxMode: cfg.Payment.AlipaySandbox,
				NotifyURL:   notifyURL + "/alipay",
			})
			l.Info("Alipay V2 adapter initialized", "sandbox", cfg.Payment.AlipaySandbox)
		}
	} else {
		mock := payment.NewAlipayMockAdapter(cfg.Payment.AlipayAppID, notifyURL+"/alipay")
		registry.Register("alipay", mock, payment.ProviderStatus{
			IsMock:    true,
			NotifyURL: notifyURL + "/alipay",
		})
		l.Info("Alipay mock adapter (no credentials)")
	}

	paymentSvc := payment.NewPaymentService(orderRepo, licRepo, registry)
	commerceObserver := observability.NewCommerceObserver(l)
	checkoutPolicies := buildCheckoutPolicies(cfg, paymentRiskFlagRepo, grantRepo, subscriptionCancellationRepo)
	var checkoutSvc service.CheckoutService = service.NewCheckoutServiceWithPolicies(orderRepo, productRepo, userRepo, paymentSvc, checkoutPolicies...)
	checkoutSvc = service.NewObservedCheckoutService(checkoutSvc, commerceObserver)
	var fulfillmentSvc service.FulfillmentService = service.NewFulfillmentService(service.FulfillmentDependencies{
		Repositories: service.FulfillmentRepositories{
			Orders:                orderRepo,
			Users:                 userRepo,
			EntitlementGrants:     grantRepo,
			CreditAccounts:        creditAccountRepo,
			CreditTransactions:    creditTransactionRepo,
			CreditBuckets:         creditBucketRepo,
			FulfillmentExecutions: fulfillmentExecutionRepo,
		},
		Catalog:            fulfillmentCatalog,
		EntitlementCatalog: entitlementCatalog,
		UnitOfWorkFactory:  uowFactory,
	})
	fulfillmentSvc = service.NewObservedFulfillmentService(fulfillmentSvc, commerceObserver)
	paymentRiskSvc := service.NewPaymentRiskService(paymentRiskFlagRepo)
	var paymentAdjustmentSvc service.PaymentAdjustmentService = service.NewPaymentAdjustmentService(service.PaymentAdjustmentDependencies{
		Repositories: service.PaymentAdjustmentRepositories{
			Orders:                orderRepo,
			EntitlementGrants:     grantRepo,
			CreditAccounts:        creditAccountRepo,
			CreditTransactions:    creditTransactionRepo,
			CreditBuckets:         creditBucketRepo,
			FulfillmentExecutions: fulfillmentExecutionRepo,
			PaymentRiskFlags:      paymentRiskFlagRepo,
		},
		Policy:            buildPaymentAdjustmentPolicy(cfg),
		UnitOfWorkFactory: uowFactory,
	})
	paymentAdjustmentSvc = service.NewObservedPaymentAdjustmentService(paymentAdjustmentSvc, commerceObserver)
	subscriptionRenewalSvc := service.NewSubscriptionRenewalService(service.SubscriptionRenewalDependencies{
		Repositories: service.SubscriptionRenewalRepositories{
			Orders:            orderRepo,
			Users:             userRepo,
			EntitlementGrants: grantRepo,
		},
		Fulfillment:        fulfillmentSvc,
		Policy:             buildSubscriptionRenewalPolicy(cfg),
		AccessPolicy:       commerceCatalog.SubscriptionAccessPolicy(),
		EntitlementCatalog: entitlementCatalog,
		UnitOfWorkFactory:  uowFactory,
	})
	paymentOrderProcessor := service.NewPaymentOrderEventProcessor(orderRepo)
	paymentEventProcessor := service.NewPaymentFulfillmentEventProcessorWithPolicies(orderRepo, paymentOrderProcessor, fulfillmentSvc, paymentAdjustmentSvc, subscriptionRenewalSvc)
	var paymentEventSvc service.PaymentEventService = service.NewPaymentEventService(paymentEventRepo, paymentSvc, paymentEventProcessor)
	paymentEventSvc = service.NewObservedPaymentEventService(paymentEventSvc, commerceObserver)
	subscriptionCancellationSvc := service.NewSubscriptionCancellationService(service.SubscriptionCancellationDependencies{
		Repositories: service.SubscriptionCancellationRepositories{
			Orders:            orderRepo,
			Users:             userRepo,
			EntitlementGrants: grantRepo,
			PaymentEvents:     paymentEventRepo,
			Cancellations:     subscriptionCancellationRepo,
		},
		UnitOfWorkFactory: uowFactory,
	})

	// 8. Init Handlers
	authHandler := handler.NewAuthHandler(licSvc, auditSvc)
	orderHandler := handler.NewOrderHandler(orderSvc, paymentSvc, licSvc, auditSvc)
	orderQueryHandler := handler.NewOrderQueryHandler(orderSvc)
	renewalHandler := handler.NewRenewalHandler(orderSvc, paymentSvc)
	adminHandler := handler.NewAdminHandler(licSvc, auditSvc)
	configHandler := handler.NewPaymentConfigHandler(paymentSvc, auditSvc)
	healthHandler := handler.NewHealthHandler(db)
	dashH := handler.NewDashboardHandler(licSvc, paymentSvc)
	entitlementHandler := handler.NewEntitlementHandler(entitlementSvc, auditSvc)
	accessSessionHandler := handler.NewAccessSessionHandler(accessSessionSvc, auditSvc)
	accessAdminHandler := handler.NewAccessAdminHandler(accessAdminSvc)
	accessSnapshotHandler := handler.NewAccessSnapshotHandler(accessSnapshotIssuer)
	creditHandler := handler.NewCreditHandler(creditSvc, auditSvc)
	checkoutHandler := handler.NewCheckoutHandler(checkoutSvc)
	subscriptionHandler := handler.NewSubscriptionHandler(subscriptionCancellationSvc)
	paymentEventHandler := handler.NewPaymentEventHandler(paymentEventSvc)
	mockCheckoutHandler := handler.NewMockCheckoutHandler(paymentEventSvc)
	paymentRiskHandler := handler.NewPaymentRiskHandler(paymentRiskSvc, auditSvc)
	fulfillmentHandler := handler.NewFulfillmentHandler(fulfillmentSvc)
	cloudStorageHandler := handler.NewCloudStorageHandler(cloudStorageSvc)

	// 9. Setup Router
	if cfg.Server.Env == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(middleware.Recovery(l))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(l))
	r.Use(metrics.Middleware())

	// Rate limiting (applied to auth endpoints only)
	enableRateLimit := cfg.RateLimit.Enabled

	// All /api/v1 routes go through a single group
	api := r.Group("/api/v1")
	if enableRateLimit {
		limiter := middleware.NewIPRateLimiter(cfg.RateLimit.MaxTokens, cfg.RateLimit.RefillRate)
		api.Use(middleware.RateLimit(limiter))
		l.Info("Rate limiting enabled", "max_tokens", cfg.RateLimit.MaxTokens, "refill_rate", cfg.RateLimit.RefillRate)
	}

	// Auth endpoints
	{
		api.POST("/verify", authHandler.Verify)
		api.POST("/activate", authHandler.Activate)
	}

	// Entitlement registration, app snapshot, and credits endpoints
	{
		api.POST("/access/registrations", accessSessionHandler.RegisterOrRestore)
		api.GET("/users/:user_id/access/snapshot", accessSnapshotHandler.GetSnapshot)
		api.POST("/registrations", entitlementHandler.SubmitRegistration)
		api.GET("/users/:user_id/entitlements/snapshot", entitlementHandler.GetUserEntitlementSnapshot)
		api.GET("/users/:user_id/credits/account", creditHandler.GetAccount)
		api.POST("/credits/reservations", creditHandler.Reserve)
		api.POST("/credits/reservations/:id/commit", creditHandler.Commit)
		api.POST("/credits/reservations/:id/release", creditHandler.Release)
	}

	// Cloud storage metadata/session facade. Desktop PC Core owns local
	// inventory and calls these endpoints; clients never talk to object storage directly.
	{
		api.POST("/cloud-storage/sync-sessions", cloudStorageHandler.AuthorizeSync)
		api.POST("/cloud-storage/manifests", cloudStorageHandler.CommitManifest)
		api.GET("/users/:user_id/cloud-storage/usage", cloudStorageHandler.Usage)
	}

	// Commerce checkout facade. Provider-specific checkout details stay inside
	// walnut-billing and project into Walnut orders before fulfillment.
	{
		api.POST("/commerce/checkout-sessions", checkoutHandler.CreateCheckoutSession)
		api.POST("/commerce/subscriptions/cancel", subscriptionHandler.Cancel)
		api.POST("/commerce/subscriptions/resume", subscriptionHandler.Resume)
	}

	// Provider-agnostic payment webhook inbox. Legacy /callbacks remains for
	// current license flows; new commerce providers should use /webhooks.
	{
		api.POST("/webhooks/:provider", paymentEventHandler.ReceiveWebhook)
	}

	// Order & Payment
	deactivateHandler := handler.NewDeactivateHandler(licSvc)
	{
		api.POST("/orders", orderHandler.CreateOrder)
		api.POST("/orders/pay", orderHandler.GetPaymentURL)
		api.GET("/orders/:out_trade_no", orderQueryHandler.GetOrder)
		api.POST("/callbacks/:provider", orderHandler.PaymentCallback)
		api.POST("/deactivate", deactivateHandler.Deactivate)
	}

	// Renewal endpoints
	{
		api.POST("/orders/renew", renewalHandler.CreateRenewalOrder)
		api.POST("/orders/renew/pay", renewalHandler.RenewAndPay)
	}

	// Admin endpoints (API Key auth required)
	admin := r.Group("/api/v1/admin")
	adminPrincipals := buildAdminPrincipals(cfg.Admin)
	adminAuthEnabled := len(adminPrincipals) > 0
	if adminAuthEnabled {
		admin.Use(middleware.APIKeyAuthPrincipals(adminPrincipals))
		l.Info("Admin API authentication enabled", "principals_count", len(adminPrincipals))
	} else if cfg.Server.Env == "prod" {
		l.Error("Admin API authentication is required in production (set ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON)")
		os.Exit(1)
	} else {
		l.Warn("Admin API authentication disabled — no admin principals configured (set ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON)")
	}
	requireAdmin := func(permission string) gin.HandlerFunc { return permissionGate(adminAuthEnabled, permission) }
	{
		admin.GET("/licenses", requireAdmin(middleware.PermissionLicensesRead), adminHandler.ListLicenses)
		admin.GET("/licenses/:key", requireAdmin(middleware.PermissionLicensesRead), adminHandler.GetLicense)
		admin.GET("/stats", requireAdmin(middleware.PermissionLicensesRead), adminHandler.Stats)
		admin.POST("/licenses/check-expiry", requireAdmin(middleware.PermissionLicensesWrite), adminHandler.CheckExpiry)
		admin.GET("/licenses/expiring", requireAdmin(middleware.PermissionLicensesRead), adminHandler.ListExpiring)

		// Payment config management (hot-reload)
		admin.GET("/payment/providers", requireAdmin(middleware.PermissionPaymentRead), configHandler.GetProviderStatus)
		admin.PUT("/payment/wechat", requireAdmin(middleware.PermissionPaymentWrite), configHandler.UpdateWechatConfig)
		admin.PUT("/payment/alipay", requireAdmin(middleware.PermissionPaymentWrite), configHandler.UpdateAlipayConfig)
		admin.PUT("/payment/creem", requireAdmin(middleware.PermissionPaymentWrite), configHandler.UpdateCreemConfig)
		admin.POST("/payment/:provider/mock", requireAdmin(middleware.PermissionPaymentWrite), configHandler.SwitchToMock)
		admin.POST("/payment/import", requireAdmin(middleware.PermissionPaymentWrite), configHandler.ImportProviders)

		// Audit logs
		admin.GET("/audit", requireAdmin(middleware.PermissionAuditRead), adminHandler.GetAuditLogs)

		// Payment webhook inbox and reprocessing
		admin.GET("/payment-events", requireAdmin(middleware.PermissionPaymentEventsRead), paymentEventHandler.ListEvents)
		admin.GET("/payment-events/:id", requireAdmin(middleware.PermissionPaymentEventsRead), paymentEventHandler.GetEvent)
		admin.POST("/payment-events/:id/reprocess", requireAdmin(middleware.PermissionPaymentEventsWrite), paymentEventHandler.ReprocessEvent)
		admin.GET("/payment-risk-flags", requireAdmin(middleware.PermissionPaymentRiskRead), paymentRiskHandler.ListFlags)
		admin.GET("/payment-risk-flags/:id", requireAdmin(middleware.PermissionPaymentRiskRead), paymentRiskHandler.GetFlag)
		admin.POST("/payment-risk-flags/:id/resolve", requireAdmin(middleware.PermissionPaymentRiskWrite), paymentRiskHandler.ResolveFlag)
		admin.GET("/fulfillments", requireAdmin(middleware.PermissionFulfillmentsRead), fulfillmentHandler.ListExecutions)

		// Entitlement registration, access-account read models, manual grants, and credits ledger
		admin.GET("/access-accounts", requireAdmin(middleware.PermissionAccessAccountsRead), accessAdminHandler.ListAccounts)
		admin.GET("/registrations", requireAdmin(middleware.PermissionRegistrationsRead), entitlementHandler.ListRegistrations)
		admin.POST("/registrations/:id/review", requireAdmin(middleware.PermissionRegistrationsWrite), entitlementHandler.ReviewRegistration)
		admin.GET("/grants", requireAdmin(middleware.PermissionEntitlementGrantsRead), entitlementHandler.ListGrants)
		admin.POST("/grants", requireAdmin(middleware.PermissionEntitlementGrantsWrite), entitlementHandler.CreateGrant)
		admin.POST("/credits/grants", requireAdmin(middleware.PermissionCreditsWrite), creditHandler.Grant)
		admin.POST("/credits/buckets/expire", requireAdmin(middleware.PermissionCreditsWrite), creditHandler.ExpireBuckets)
		admin.GET("/users/:user_id/credits/transactions", requireAdmin(middleware.PermissionCreditsRead), creditHandler.ListTransactions)
		admin.GET("/users/:user_id/credits/usage-records", requireAdmin(middleware.PermissionCreditsRead), creditHandler.ListUsageRecords)
	}

	// License status (public, no auth needed — client-facing)
	{
		api.GET("/licenses/:key/status", adminHandler.LicenseStatus)
	}

	if cfg.Server.Env != "prod" {
		r.GET("/checkout/:out_trade_no", mockCheckoutHandler.Show)
		r.POST("/checkout/:out_trade_no/complete", mockCheckoutHandler.Complete)
	}

	// Health checks
	r.GET("/ping", healthHandler.Ping)
	r.GET("/health", healthHandler.Health)
	r.GET("/metrics", metrics.Handler())

	// Dashboard (embedded SPA)
	r.GET("/dashboard", handler.ServeDashboard)
	admin.GET("/dashboard", requireAdmin(middleware.PermissionDashboardRead), dashH.GetDashboard)

	// 10. Start Server with Graceful Shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.Server.Port,
		Handler: r,
	}

	go func() {
		l.Info("Server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			l.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	l.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		l.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	// Stop audit writer (drains remaining entries)
	auditSvc.(interface{ Stop() }).Stop()

	l.Info("Server exited cleanly")
}
