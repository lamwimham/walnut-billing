package main

import (
	"context"
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
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
	gorm_repo "walnut-billing/internal/repository/gorm_repo"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
		&domain.CreditAccount{},
		&domain.CreditReservation{},
		&domain.CreditTransaction{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
	); err != nil {
		l.Error("Failed to migrate database", "error", err)
		os.Exit(1)
	}

	// 3. Seed default products
	gorm_repo.SeedProducts(db)

	// 4. Init Repositories
	licRepo := &gorm_repo.LicenseRepo{DB: db}
	orderRepo := &gorm_repo.OrderRepo{DB: db}
	productRepo := &gorm_repo.ProductRepo{DB: db}
	auditRepo := &gorm_repo.AuditRepo{DB: db}
	userRepo := &gorm_repo.UserRepo{DB: db}
	registrationRepo := &gorm_repo.RegistrationRepo{DB: db}
	grantRepo := &gorm_repo.EntitlementGrantRepo{DB: db}
	creditAccountRepo := &gorm_repo.CreditAccountRepo{DB: db}
	creditReservationRepo := &gorm_repo.CreditReservationRepo{DB: db}
	creditTransactionRepo := &gorm_repo.CreditTransactionRepo{DB: db}
	paymentEventRepo := &gorm_repo.PaymentEventRepo{DB: db}
	fulfillmentExecutionRepo := &gorm_repo.FulfillmentExecutionRepo{DB: db}

	// 5. Init Key Generator Factory
	keyFactory := generator.DefaultFactory()

	// 6. Init Services
	licSvc := service.NewLicenseService(licRepo)
	auditSvc := service.NewAuditService(auditRepo, 100, slog.Default())
	uowFactory := func() repository.UnitOfWork {
		return gorm_repo.NewUnitOfWork(db)
	}
	orderSvc := service.NewOrderService(orderRepo, productRepo, licRepo, keyFactory, uowFactory)
	creditSvc := service.NewCreditService(userRepo, creditAccountRepo, creditReservationRepo, creditTransactionRepo, uowFactory)
	entitlementSvc := service.NewEntitlementServiceWithCredits(userRepo, registrationRepo, grantRepo, creditAccountRepo, service.DefaultEntitlementCatalog())
	fulfillmentCatalog, err := service.NewFulfillmentCatalogFromJSON(cfg.Fulfillment.RulesJSON, service.DefaultFulfillmentRules())
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

	if cfg.Server.Env != "prod" {
		registry.Register("mock", payment.NewCheckoutMockAdapter(notifyURL+"/mock"), payment.ProviderStatus{
			IsMock:    true,
			NotifyURL: notifyURL + "/mock",
		})
		l.Info("Generic checkout mock adapter initialized")
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
	checkoutSvc := service.NewCheckoutService(orderRepo, productRepo, userRepo, paymentSvc)
	fulfillmentSvc := service.NewFulfillmentService(service.FulfillmentDependencies{
		Repositories: service.FulfillmentRepositories{
			Orders:                orderRepo,
			Users:                 userRepo,
			EntitlementGrants:     grantRepo,
			CreditAccounts:        creditAccountRepo,
			CreditTransactions:    creditTransactionRepo,
			FulfillmentExecutions: fulfillmentExecutionRepo,
		},
		Catalog:            fulfillmentCatalog,
		EntitlementCatalog: service.DefaultEntitlementCatalog(),
		UnitOfWorkFactory:  uowFactory,
	})
	paymentOrderProcessor := service.NewPaymentOrderEventProcessor(orderRepo)
	paymentEventProcessor := service.NewPaymentFulfillmentEventProcessor(orderRepo, paymentOrderProcessor, fulfillmentSvc)
	paymentEventSvc := service.NewPaymentEventService(paymentEventRepo, paymentSvc, paymentEventProcessor)

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
	creditHandler := handler.NewCreditHandler(creditSvc, auditSvc)
	checkoutHandler := handler.NewCheckoutHandler(checkoutSvc)
	paymentEventHandler := handler.NewPaymentEventHandler(paymentEventSvc)
	fulfillmentHandler := handler.NewFulfillmentHandler(fulfillmentSvc)

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
		api.POST("/registrations", entitlementHandler.SubmitRegistration)
		api.GET("/users/:user_id/entitlements/snapshot", entitlementHandler.GetUserEntitlementSnapshot)
		api.GET("/users/:user_id/credits/account", creditHandler.GetAccount)
		api.POST("/credits/reservations", creditHandler.Reserve)
		api.POST("/credits/reservations/:id/commit", creditHandler.Commit)
		api.POST("/credits/reservations/:id/release", creditHandler.Release)
	}

	// Commerce checkout facade. Provider-specific checkout details stay inside
	// walnut-billing and project into Walnut orders before fulfillment.
	{
		api.POST("/commerce/checkout-sessions", checkoutHandler.CreateCheckoutSession)
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
	if len(cfg.Admin.APIKeys) > 0 {
		admin.Use(middleware.APIKeyAuth(cfg.Admin.APIKeys))
		l.Info("Admin API authentication enabled", "keys_count", len(cfg.Admin.APIKeys))
	} else {
		l.Warn("Admin API authentication disabled — no API keys configured (set ADMIN_API_KEYS env var)")
	}
	{
		admin.GET("/licenses", adminHandler.ListLicenses)
		admin.GET("/licenses/:key", adminHandler.GetLicense)
		admin.GET("/stats", adminHandler.Stats)
		admin.POST("/licenses/check-expiry", adminHandler.CheckExpiry)
		admin.GET("/licenses/expiring", adminHandler.ListExpiring)

		// Payment config management (hot-reload)
		admin.GET("/payment/providers", configHandler.GetProviderStatus)
		admin.PUT("/payment/wechat", configHandler.UpdateWechatConfig)
		admin.PUT("/payment/alipay", configHandler.UpdateAlipayConfig)
		admin.POST("/payment/:provider/mock", configHandler.SwitchToMock)
		admin.POST("/payment/import", configHandler.ImportProviders)

		// Audit logs
		admin.GET("/audit", adminHandler.GetAuditLogs)

		// Payment webhook inbox and reprocessing
		admin.GET("/payment-events", paymentEventHandler.ListEvents)
		admin.GET("/payment-events/:id", paymentEventHandler.GetEvent)
		admin.POST("/payment-events/:id/reprocess", paymentEventHandler.ReprocessEvent)
		admin.GET("/fulfillments", fulfillmentHandler.ListExecutions)

		// Entitlement registration, manual grants, and credits ledger
		admin.GET("/registrations", entitlementHandler.ListRegistrations)
		admin.POST("/registrations/:id/review", entitlementHandler.ReviewRegistration)
		admin.GET("/grants", entitlementHandler.ListGrants)
		admin.POST("/grants", entitlementHandler.CreateGrant)
		admin.POST("/credits/grants", creditHandler.Grant)
		admin.GET("/users/:user_id/credits/transactions", creditHandler.ListTransactions)
		admin.GET("/users/:user_id/credits/usage-records", creditHandler.ListUsageRecords)
	}

	// License status (public, no auth needed — client-facing)
	{
		api.GET("/licenses/:key/status", adminHandler.LicenseStatus)
	}

	// Health checks
	r.GET("/ping", healthHandler.Ping)
	r.GET("/health", healthHandler.Health)
	r.GET("/metrics", metrics.Handler())

	// Dashboard (embedded SPA)
	r.GET("/dashboard", handler.ServeDashboard)
	admin.GET("/dashboard", dashH.GetDashboard)

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
