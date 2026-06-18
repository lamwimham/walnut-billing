package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
	gorm_repo "walnut-billing/internal/repository/gorm_repo"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCommerceFlow_GormCheckoutWebhookSnapshotDisputeRiskHold(t *testing.T) {
	ctx := context.Background()
	db := openCommerceFlowTestDB(t)
	repos := newCommerceFlowGormRepos(db)
	if err := repos.users.Create(ctx, &domain.User{ID: "usr_1", Email: "writer@example.com", DisplayName: "Writer", Status: domain.UserStatusActive}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repos.products.Create(ctx, &domain.Product{Code: "editorial_studio_monthly", Name: "Editorial Studio", Price: 1900, Currency: "USD", Validity: "monthly", IsVisible: true}); err != nil {
		t.Fatalf("create product: %v", err)
	}

	checkoutCalls := &atomic.Int64{}
	creemServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkoutCalls.Add(1)
		if r.URL.Path != "/v1/checkouts" || r.Header.Get("x-api-key") != "creem_test_key" {
			t.Fatalf("unexpected creem checkout request path=%s apiKey=%s", r.URL.Path, r.Header.Get("x-api-key"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode checkout payload: %v", err)
		}
		outTradeNo, _ := payload["request_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"ch_%s","checkout_url":"https://checkout.creem.io/ch_%s","customer_id":"cust_1"}`, outTradeNo, outTradeNo)
	}))
	defer creemServer.Close()

	paymentSvc := newCommerceFlowPaymentService(t, creemServer.URL, repos.orders)
	checkoutSvc := NewCheckoutServiceWithPolicies(
		repos.orders,
		repos.products,
		repos.users,
		paymentSvc,
		NewPaymentRiskCheckoutPolicy(repos.risks, DefaultCheckoutRiskPolicyConfig()),
	)
	fulfillmentCatalog, err := NewStaticFulfillmentCatalog(editorialStudioFulfillmentRules()...)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	uowFactory := func() repository.UnitOfWork { return gorm_repo.NewUnitOfWork(db) }
	fulfillmentSvc := NewFulfillmentService(FulfillmentDependencies{
		Repositories: FulfillmentRepositories{
			Orders:                repos.orders,
			Users:                 repos.users,
			EntitlementGrants:     repos.grants,
			CreditAccounts:        repos.accounts,
			CreditTransactions:    repos.transactions,
			CreditBuckets:         repos.buckets,
			FulfillmentExecutions: repos.executions,
		},
		Catalog:            fulfillmentCatalog,
		EntitlementCatalog: DefaultEntitlementCatalog(),
		UnitOfWorkFactory:  uowFactory,
	})
	adjustmentSvc := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                repos.orders,
			EntitlementGrants:     repos.grants,
			CreditAccounts:        repos.accounts,
			CreditTransactions:    repos.transactions,
			CreditBuckets:         repos.buckets,
			FulfillmentExecutions: repos.executions,
			PaymentRiskFlags:      repos.risks,
		},
		UnitOfWorkFactory: uowFactory,
	})
	eventSvc := NewPaymentEventService(
		repos.events,
		paymentSvc,
		NewPaymentFulfillmentEventProcessorWithAdjustments(repos.orders, NewPaymentOrderEventProcessor(repos.orders), fulfillmentSvc, adjustmentSvc),
	)
	entitlementSvc := NewEntitlementServiceWithCredits(repos.users, repos.registrations, repos.grants, repos.accounts, DefaultEntitlementCatalog())

	checkout, err := checkoutSvc.CreateCheckoutSession(ctx, CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "creem",
		IdempotencyKey: "checkout:gorm:1",
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if checkout.Order.OutTradeNo == "" || checkout.CheckoutURL == "" || checkoutCalls.Load() != 1 {
		t.Fatalf("unexpected checkout result=%#v calls=%d", checkout, checkoutCalls.Load())
	}

	paidPayload := creemPaidWebhookPayload(checkout.Order.OutTradeNo, "evt_gorm_paid_1")
	paid, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(paidPayload, "whsec_test")},
		RawPayload: paidPayload,
	})
	if err != nil {
		t.Fatalf("paid webhook: %v", err)
	}
	if !paid.Processed || paid.Duplicate {
		t.Fatalf("expected first paid event to process, got %#v", paid)
	}
	storedOrder, err := repos.orders.GetByOutTradeNo(ctx, checkout.Order.OutTradeNo)
	if err != nil {
		t.Fatalf("get fulfilled order: %v", err)
	}
	if storedOrder.Status != domain.OrderStatusFulfilled || storedOrder.PaidAt == nil || storedOrder.FulfilledAt == nil {
		t.Fatalf("expected fulfilled order, got %#v", storedOrder)
	}
	snapshot, err := entitlementSvc.SnapshotForUser(ctx, "usr_1")
	if err != nil {
		t.Fatalf("snapshot after paid: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] || snapshot.Credits[domain.CreditMetricBalance] != 600 {
		t.Fatalf("expected entitlement+credits after paid, got %#v", snapshot)
	}

	duplicatePaid, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(paidPayload, "whsec_test")},
		RawPayload: paidPayload,
	})
	if err != nil {
		t.Fatalf("duplicate paid webhook: %v", err)
	}
	if !duplicatePaid.Duplicate || !duplicatePaid.Processed {
		t.Fatalf("expected duplicate paid event to be idempotent, got %#v", duplicatePaid)
	}
	assertCommerceFlowCounts(t, db, 1, 1, 2)

	disputePayload := creemDisputeWebhookPayload(checkout.Order.OutTradeNo, "evt_gorm_dispute_1")
	dispute, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(disputePayload, "whsec_test")},
		RawPayload: disputePayload,
	})
	if err != nil {
		t.Fatalf("dispute webhook: %v", err)
	}
	if !dispute.Processed || dispute.Event.EventType != domain.PaymentEventTypeDisputed {
		t.Fatalf("expected dispute event to process, got %#v", dispute)
	}
	afterDispute, err := entitlementSvc.SnapshotForUser(ctx, "usr_1")
	if err != nil {
		t.Fatalf("snapshot after dispute: %v", err)
	}
	if afterDispute.Entitlements[domain.EntitlementEditorialStudio] || afterDispute.Credits[domain.CreditMetricBalance] != 0 {
		t.Fatalf("expected dispute to revoke entitlement and claw back credits, got %#v", afterDispute)
	}
	risks, err := repos.risks.List(ctx, repository.PaymentRiskFlagQuery{UserID: "usr_1", Status: domain.PaymentRiskStatusOpen})
	if err != nil {
		t.Fatalf("list risks: %v", err)
	}
	if len(risks) != 1 || risks[0].Severity != domain.PaymentRiskSeverityCritical {
		t.Fatalf("expected one open critical risk, got %#v", risks)
	}

	_, err = checkoutSvc.CreateCheckoutSession(ctx, CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "creem",
		IdempotencyKey: "checkout:gorm:blocked",
	})
	if !errors.Is(err, ErrCheckoutBlockedByRisk) {
		t.Fatalf("expected checkout risk hold, got %v", err)
	}
	if checkoutCalls.Load() != 1 {
		t.Fatalf("blocked checkout must not call provider, got calls=%d", checkoutCalls.Load())
	}
}

func TestCommerceFlow_GormSubscriptionRenewalPaidCreatesNewPeriod(t *testing.T) {
	ctx := context.Background()
	db := openCommerceFlowTestDB(t)
	repos := newCommerceFlowGormRepos(db)
	if err := repos.users.Create(ctx, &domain.User{ID: "usr_renew_paid", Email: "renew-paid@example.com", Status: domain.UserStatusActive}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repos.products.Create(ctx, &domain.Product{Code: "editorial_studio_monthly", Name: "Editorial Studio", Price: 1900, Currency: "USD", Validity: "monthly", IsVisible: true}); err != nil {
		t.Fatalf("create product: %v", err)
	}
	paymentSvc := newCommerceFlowPaymentService(t, "http://127.0.0.1", repos.orders)
	fulfillmentSvc, eventSvc, entitlementSvc := newCommerceFlowRenewalServices(t, db, repos, paymentSvc)

	paidAt := time.Now().UTC().AddDate(0, -1, 0).Add(-time.Hour)
	initial := &domain.Order{
		OutTradeNo: "CHK-GORM-RENEW-PAID",
		UserID:     "usr_renew_paid",
		SKUCode:    "editorial_studio_monthly",
		Amount:     1900,
		Currency:   "USD",
		Status:     domain.OrderStatusPaid,
		OrderType:  domain.OrderTypeCheckout,
		PaidAt:     &paidAt,
	}
	if err := repos.orders.Create(ctx, initial); err != nil {
		t.Fatalf("create initial order: %v", err)
	}
	if _, err := fulfillmentSvc.FulfillOrder(ctx, initial); err != nil {
		t.Fatalf("initial fulfillment: %v", err)
	}
	initialExpiry := paidAt.AddDate(0, 1, 0)
	renewalEnd := initialExpiry.AddDate(0, 1, 0)

	payload := creemSubscriptionWebhookPayload(initial.OutTradeNo, "evt_gorm_subscription_paid_1", "subscription.paid", initialExpiry, renewalEnd)
	paid, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(payload, "whsec_test")},
		RawPayload: payload,
	})
	if err != nil {
		t.Fatalf("renewal paid webhook: %v", err)
	}
	if !paid.Processed || paid.Event.EventType != domain.PaymentEventTypeRenewalPaid {
		t.Fatalf("expected renewal paid event, got %#v", paid)
	}
	assertCommerceFlowCounts(t, db, 2, 2, 4)
	snapshot, err := entitlementSvc.SnapshotForUser(ctx, "usr_renew_paid")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] || snapshot.Credits[domain.CreditMetricBalance] != 1200 {
		t.Fatalf("expected renewed entitlement and second period credits, got %#v", snapshot)
	}
}

func TestCommerceFlow_GormSubscriptionRenewalFailedGraceAndExpired(t *testing.T) {
	ctx := context.Background()
	db := openCommerceFlowTestDB(t)
	repos := newCommerceFlowGormRepos(db)
	if err := repos.users.Create(ctx, &domain.User{ID: "usr_renew_grace", Email: "renew-grace@example.com", Status: domain.UserStatusActive}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repos.products.Create(ctx, &domain.Product{Code: "editorial_studio_monthly", Name: "Editorial Studio", Price: 1900, Currency: "USD", Validity: "monthly", IsVisible: true}); err != nil {
		t.Fatalf("create product: %v", err)
	}
	paymentSvc := newCommerceFlowPaymentService(t, "http://127.0.0.1", repos.orders)
	fulfillmentSvc, eventSvc, entitlementSvc := newCommerceFlowRenewalServices(t, db, repos, paymentSvc)

	paidAt := time.Now().UTC().AddDate(0, -1, 0).Add(-time.Hour)
	initial := &domain.Order{
		OutTradeNo: "CHK-GORM-RENEW-GRACE",
		UserID:     "usr_renew_grace",
		SKUCode:    "editorial_studio_monthly",
		Amount:     1900,
		Currency:   "USD",
		Status:     domain.OrderStatusPaid,
		OrderType:  domain.OrderTypeCheckout,
		PaidAt:     &paidAt,
	}
	if err := repos.orders.Create(ctx, initial); err != nil {
		t.Fatalf("create initial order: %v", err)
	}
	if _, err := fulfillmentSvc.FulfillOrder(ctx, initial); err != nil {
		t.Fatalf("initial fulfillment: %v", err)
	}
	initialExpiry := paidAt.AddDate(0, 1, 0)
	graceEnd := initialExpiry.AddDate(0, 0, domain.GracePeriodDays)

	pastDuePayload := creemSubscriptionWebhookPayload(initial.OutTradeNo, "evt_gorm_subscription_past_due_1", "subscription.past_due", paidAt, initialExpiry)
	failed, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(pastDuePayload, "whsec_test")},
		RawPayload: pastDuePayload,
	})
	if err != nil {
		t.Fatalf("renewal failed webhook: %v", err)
	}
	if !failed.Processed || failed.Event.EventType != domain.PaymentEventTypeRenewalFailed {
		t.Fatalf("expected renewal failed event, got %#v", failed)
	}
	assertCommerceFlowCounts(t, db, 2, 1, 2)
	snapshot, err := entitlementSvc.SnapshotForUser(ctx, "usr_renew_grace")
	if err != nil {
		t.Fatalf("snapshot during grace: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] || snapshot.Credits[domain.CreditMetricBalance] != 600 {
		t.Fatalf("expected grace access without new credits, got %#v", snapshot)
	}

	earlyExpiredPayload := creemSubscriptionWebhookPayload(initial.OutTradeNo, "evt_gorm_subscription_expired_early_1", "subscription.expired", initialExpiry, initialExpiry)
	earlyExpired, err := eventSvc.ReceiveWebhook(ctx, PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(earlyExpiredPayload, "whsec_test")},
		RawPayload: earlyExpiredPayload,
	})
	if err != nil {
		t.Fatalf("early subscription expired webhook: %v", err)
	}
	if !earlyExpired.Processed || earlyExpired.Event.EventType != domain.PaymentEventTypeSubscriptionExpired {
		t.Fatalf("expected early subscription expired event, got %#v", earlyExpired)
	}
	duringGraceAfterExpired, err := entitlementSvc.SnapshotForUser(ctx, "usr_renew_grace")
	if err != nil {
		t.Fatalf("snapshot during grace after expired: %v", err)
	}
	if !duringGraceAfterExpired.Entitlements[domain.EntitlementEditorialStudio] || duringGraceAfterExpired.Credits[domain.CreditMetricBalance] != 600 {
		t.Fatalf("expected early subscription.expired to preserve grace access, got %#v", duringGraceAfterExpired)
	}

	expiredPayload := creemSubscriptionWebhookPayload(initial.OutTradeNo, "evt_gorm_subscription_expired_1", "subscription.expired", paidAt, initialExpiry)
	expiredEvent := mustPaymentEventFromWebhookPayload(t, paymentSvc, expiredPayload)
	expiredEvent.ID = "pev_gorm_subscription_expired_1"
	expiredEvent.Provider = "creem"
	expiredEvent.ReceivedAt = graceEnd.Add(time.Second)
	expiredEvent.CreatedAt = expiredEvent.ReceivedAt
	expiredEvent.UpdatedAt = expiredEvent.ReceivedAt
	if err := repos.events.Create(ctx, expiredEvent); err != nil {
		t.Fatalf("create delayed expired event: %v", err)
	}
	expired, err := eventSvc.Process(ctx, expiredEvent.ID)
	if err != nil {
		t.Fatalf("process delayed subscription expired event: %v", err)
	}
	if !expired.Processed || expired.Event.EventType != domain.PaymentEventTypeSubscriptionExpired {
		t.Fatalf("expected delayed subscription expired event, got %#v", expired)
	}
	afterExpired, err := entitlementSvc.SnapshotForUser(ctx, "usr_renew_grace")
	if err != nil {
		t.Fatalf("snapshot after expired: %v", err)
	}
	if afterExpired.Entitlements[domain.EntitlementEditorialStudio] || afterExpired.Credits[domain.CreditMetricBalance] != 600 {
		t.Fatalf("expected grace access expired without credit clawback, got %#v", afterExpired)
	}
}

type commerceFlowGormRepos struct {
	orders        *gorm_repo.OrderRepo
	products      *gorm_repo.ProductRepo
	users         *gorm_repo.UserRepo
	registrations *gorm_repo.RegistrationRepo
	grants        *gorm_repo.EntitlementGrantRepo
	accounts      *gorm_repo.CreditAccountRepo
	buckets       *gorm_repo.CreditBucketRepo
	transactions  *gorm_repo.CreditTransactionRepo
	executions    *gorm_repo.FulfillmentExecutionRepo
	events        *gorm_repo.PaymentEventRepo
	risks         *gorm_repo.PaymentRiskFlagRepo
}

func openCommerceFlowTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.Product{},
		&domain.Order{},
		&domain.EntitlementGrant{},
		&domain.CreditAccount{},
		&domain.CreditBucket{},
		&domain.CreditReservation{},
		&domain.CreditTransaction{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
		&domain.PaymentRiskFlag{},
	); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func newCommerceFlowGormRepos(db *gorm.DB) commerceFlowGormRepos {
	return commerceFlowGormRepos{
		orders:        &gorm_repo.OrderRepo{DB: db},
		products:      &gorm_repo.ProductRepo{DB: db},
		users:         &gorm_repo.UserRepo{DB: db},
		registrations: &gorm_repo.RegistrationRepo{DB: db},
		grants:        &gorm_repo.EntitlementGrantRepo{DB: db},
		accounts:      &gorm_repo.CreditAccountRepo{DB: db},
		buckets:       &gorm_repo.CreditBucketRepo{DB: db},
		transactions:  &gorm_repo.CreditTransactionRepo{DB: db},
		executions:    &gorm_repo.FulfillmentExecutionRepo{DB: db},
		events:        &gorm_repo.PaymentEventRepo{DB: db},
		risks:         &gorm_repo.PaymentRiskFlagRepo{DB: db},
	}
}

func newCommerceFlowPaymentService(t *testing.T, creemBaseURL string, orders repository.OrderRepository) *payment.PaymentService {
	t.Helper()
	creemAdapter, err := payment.NewCreemAdapter(payment.CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		APIBaseURL:    creemBaseURL,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		t.Fatalf("create creem adapter: %v", err)
	}
	registry := payment.NewProviderRegistry()
	registry.Register("creem", creemAdapter, payment.ProviderStatus{SandboxMode: true})
	return payment.NewPaymentService(orders, nil, registry)
}

func newCommerceFlowRenewalServices(
	t *testing.T,
	db *gorm.DB,
	repos commerceFlowGormRepos,
	paymentSvc *payment.PaymentService,
) (FulfillmentService, PaymentEventService, EntitlementService) {
	t.Helper()
	fulfillmentCatalog, err := NewStaticFulfillmentCatalog(editorialStudioFulfillmentRules()...)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	uowFactory := func() repository.UnitOfWork { return gorm_repo.NewUnitOfWork(db) }
	fulfillmentSvc := NewFulfillmentService(FulfillmentDependencies{
		Repositories: FulfillmentRepositories{
			Orders:                repos.orders,
			Users:                 repos.users,
			EntitlementGrants:     repos.grants,
			CreditAccounts:        repos.accounts,
			CreditTransactions:    repos.transactions,
			CreditBuckets:         repos.buckets,
			FulfillmentExecutions: repos.executions,
		},
		Catalog:            fulfillmentCatalog,
		EntitlementCatalog: DefaultEntitlementCatalog(),
		UnitOfWorkFactory:  uowFactory,
	})
	renewalSvc := NewSubscriptionRenewalService(SubscriptionRenewalDependencies{
		Repositories: SubscriptionRenewalRepositories{
			Orders:            repos.orders,
			Users:             repos.users,
			EntitlementGrants: repos.grants,
		},
		Fulfillment:        fulfillmentSvc,
		EntitlementCatalog: DefaultEntitlementCatalog(),
		UnitOfWorkFactory:  uowFactory,
	})
	eventSvc := NewPaymentEventService(
		repos.events,
		paymentSvc,
		NewPaymentFulfillmentEventProcessorWithPolicies(repos.orders, NewPaymentOrderEventProcessor(repos.orders), fulfillmentSvc, nil, renewalSvc),
	)
	entitlementSvc := NewEntitlementServiceWithCredits(repos.users, repos.registrations, repos.grants, repos.accounts, DefaultEntitlementCatalog())
	return fulfillmentSvc, eventSvc, entitlementSvc
}

func creemSubscriptionWebhookPayload(outTradeNo string, eventID string, eventType string, periodStart time.Time, periodEnd time.Time) []byte {
	return []byte(fmt.Sprintf(
		`{"id":"%s","eventType":"%s","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"%s"}},"order":{"id":"ord_%s","amount":1900,"currency":"USD"},"current_period_start_date":"%s","current_period_end_date":"%s"}}`,
		eventID,
		eventType,
		outTradeNo,
		eventID,
		periodStart.UTC().Format(time.RFC3339Nano),
		periodEnd.UTC().Format(time.RFC3339Nano),
	))
}

func mustPaymentEventFromWebhookPayload(t *testing.T, paymentSvc PaymentWebhookGateway, payload []byte) *domain.PaymentEventInbox {
	t.Helper()
	verified, err := paymentSvc.VerifyWebhookEvent(context.Background(), "creem", payment.WebhookVerificationRequest{
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(payload, "whsec_test")},
		RawPayload: payload,
	})
	if err != nil {
		t.Fatalf("verify webhook payload: %v", err)
	}
	now := time.Now().UTC()
	return &domain.PaymentEventInbox{
		ProviderEventID:   verified.ProviderEventID,
		EventType:         verified.EventType,
		OutTradeNo:        verified.OutTradeNo,
		ProviderTradeNo:   verified.ProviderTradeNo,
		Amount:            verified.Amount,
		Currency:          verified.Currency,
		PeriodStartAt:     verified.PeriodStartAt,
		PeriodEndAt:       verified.PeriodEndAt,
		SignatureVerified: verified.SignatureVerified,
		RawPayload:        string(payload),
		PayloadHash:       payloadHash(string(payload)),
		Status:            domain.PaymentEventStatusReceived,
		ReceivedAt:        now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func assertCommerceFlowCounts(t *testing.T, db *gorm.DB, wantGrants int64, wantCreditTransactions int64, wantExecutions int64) {
	t.Helper()
	var grants int64
	if err := db.Model(&domain.EntitlementGrant{}).Count(&grants).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	var creditTransactions int64
	if err := db.Model(&domain.CreditTransaction{}).Count(&creditTransactions).Error; err != nil {
		t.Fatalf("count credit transactions: %v", err)
	}
	var executions int64
	if err := db.Model(&domain.FulfillmentExecution{}).Count(&executions).Error; err != nil {
		t.Fatalf("count fulfillment executions: %v", err)
	}
	if grants != wantGrants || creditTransactions != wantCreditTransactions || executions != wantExecutions {
		t.Fatalf("unexpected persisted counts grants=%d txs=%d executions=%d", grants, creditTransactions, executions)
	}
}
