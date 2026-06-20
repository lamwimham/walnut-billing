package gorm_repo

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminTestScenarioResetRepoDryRunDoesNotDelete(t *testing.T) {
	db := newAdminTestScenarioResetDB(t)
	seedAdminTestScenarioResetUser(t, db)

	record, err := (&AdminTestScenarioResetRepo{DB: db}).ResetUserControlPlane(context.Background(), repository.AdminTestScenarioResetQuery{
		Scenario: "user_control_plane",
		Email:    "writer@example.com",
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("dry-run reset: %v", err)
	}
	if record.User == nil || record.User.ID != "usr_1" {
		t.Fatalf("expected matched user, got %#v", record.User)
	}
	if record.AffectedCounts["users"] != 1 || record.AffectedCounts["orders"] != 1 || record.AffectedCounts["payment_event_inboxes"] != 1 {
		t.Fatalf("unexpected dry-run counts: %#v", record.AffectedCounts)
	}
	var users int64
	if err := db.Model(&domain.User{}).Count(&users).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Fatalf("dry-run should not delete user, count=%d", users)
	}
}

func TestAdminTestScenarioResetRepoDeletesScenarioGraph(t *testing.T) {
	db := newAdminTestScenarioResetDB(t)
	seedAdminTestScenarioResetUser(t, db)

	record, err := (&AdminTestScenarioResetRepo{DB: db}).ResetUserControlPlane(context.Background(), repository.AdminTestScenarioResetQuery{
		Scenario: "user_control_plane",
		UserID:   "usr_1",
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if record.AffectedCounts["users"] != 1 || record.AffectedCounts["cloud_objects"] != 1 || record.AffectedCounts["credit_transactions"] != 1 {
		t.Fatalf("unexpected reset counts: %#v", record.AffectedCounts)
	}
	assertAdminResetTableEmpty[domain.User](t, db)
	assertAdminResetTableEmpty[domain.Order](t, db)
	assertAdminResetTableEmpty[domain.PaymentEventInbox](t, db)
	assertAdminResetTableEmpty[domain.FulfillmentExecution](t, db)
	assertAdminResetTableEmpty[domain.PaymentRiskFlag](t, db)
	assertAdminResetTableEmpty[domain.SubscriptionCancellation](t, db)
	assertAdminResetTableEmpty[domain.CloudObject](t, db)
	assertAdminResetTableEmpty[domain.CloudManifest](t, db)
	assertAdminResetTableEmpty[domain.CloudSyncSession](t, db)
	assertAdminResetTableEmpty[domain.CloudProject](t, db)
	assertAdminResetTableEmpty[domain.CreditTransaction](t, db)
	assertAdminResetTableEmpty[domain.CreditReservation](t, db)
	assertAdminResetTableEmpty[domain.CreditBucket](t, db)
	assertAdminResetTableEmpty[domain.CreditAccount](t, db)
	assertAdminResetTableEmpty[domain.UserDevice](t, db)
	assertAdminResetTableEmpty[domain.TrialGrant](t, db)
	assertAdminResetTableEmpty[domain.EntitlementGrant](t, db)
	assertAdminResetTableEmpty[domain.RegistrationRequest](t, db)
	assertAdminResetTableEmpty[domain.AccessLoginChallenge](t, db)
}

func TestAdminTestScenarioResetRepoEmailOnlyDeletesLoginChallenges(t *testing.T) {
	db := newAdminTestScenarioResetDB(t)
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.AccessLoginChallenge{ID: "alc_email_only", Email: "new@example.com", DeviceID: "device-1", TokenHash: "hash", Status: domain.AccessLoginChallengeStatusPending, IdempotencyKey: "login:new", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create challenge: %v", err)
	}

	record, err := (&AdminTestScenarioResetRepo{DB: db}).ResetUserControlPlane(context.Background(), repository.AdminTestScenarioResetQuery{
		Scenario: "user_control_plane",
		Email:    "new@example.com",
	})
	if err != nil {
		t.Fatalf("reset email-only scenario: %v", err)
	}
	if record.User != nil || record.AffectedCounts["access_login_challenges"] != 1 {
		t.Fatalf("expected email-only challenge reset, got %#v", record)
	}
	assertAdminResetTableEmpty[domain.AccessLoginChallenge](t, db)
}

func newAdminTestScenarioResetDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.AccessLoginChallenge{},
		&domain.RegistrationRequest{},
		&domain.TrialGrant{},
		&domain.EntitlementGrant{},
		&domain.UserDevice{},
		&domain.Order{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
		&domain.PaymentRiskFlag{},
		&domain.SubscriptionCancellation{},
		&domain.CloudProject{},
		&domain.CloudSyncSession{},
		&domain.CloudManifest{},
		&domain.CloudObject{},
		&domain.CreditAccount{},
		&domain.CreditBucket{},
		&domain.CreditReservation{},
		&domain.CreditTransaction{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedAdminTestScenarioResetUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * 24 * time.Hour)
	if err := db.Create(&domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&domain.AccessLoginChallenge{ID: "alc_1", Email: "writer@example.com", DeviceID: "device-1", TokenHash: "hash", Status: domain.AccessLoginChallengeStatusPending, IdempotencyKey: "login:1", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	if err := db.Create(&domain.RegistrationRequest{ID: "reg_1", UserID: "usr_1", Email: "writer@example.com", RequestedEntitlement: domain.EntitlementEditorialStudio, Status: domain.RegistrationStatusPending, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create registration: %v", err)
	}
	if err := db.Create(&domain.TrialGrant{ID: "trial_1", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, StartsAt: now, ExpiresAt: &expiresAt, IdempotencyKey: "trial:1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create trial: %v", err)
	}
	if err := db.Create(&domain.EntitlementGrant{ID: "grant_1", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, StartsAt: now, ExpiresAt: &expiresAt, IdempotencyKey: stringPtr("grant:1"), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if err := db.Create(&domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, FirstSeenAt: now, LastSeenAt: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := db.Create(&domain.Order{OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "mock", OrderType: domain.OrderTypeCheckout, PaidAt: &now, FulfilledAt: &now, IdempotencyKey: stringPtr("order:1")}).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := db.Create(&domain.PaymentEventInbox{ID: "pev_1", Provider: "mock", ProviderEventID: "evt_1", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-1", Status: domain.PaymentEventStatusProcessed, PayloadHash: "hash", ReceivedAt: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := db.Create(&domain.FulfillmentExecution{ID: "ful_1", OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, IdempotencyKey: "ful:1", Status: domain.FulfillmentExecutionStatusApplied, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create fulfillment: %v", err)
	}
	if err := db.Create(&domain.PaymentRiskFlag{ID: "risk_1", UserID: "usr_1", OutTradeNo: "CHK-1", Provider: "mock", ProviderEventID: "evt_risk_1", Status: domain.PaymentRiskStatusOpen, Reason: domain.PaymentRiskReasonDispute, Severity: domain.PaymentRiskSeverityCritical, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create risk: %v", err)
	}
	if err := db.Create(&domain.SubscriptionCancellation{ID: "sub_cancel_1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.SubscriptionStatusCancelAtPeriodEnd, CancelAtPeriodEnd: true, CurrentPeriodEndsAt: expiresAt, SourceOrderNo: "CHK-1", IdempotencyKey: "cancel:1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create cancellation: %v", err)
	}
	if err := db.Create(&domain.CloudProject{ID: "cpr_1", UserID: "usr_1", ClientProjectID: "local-project", Name: "Local Project", Status: domain.CloudProjectStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := db.Create(&domain.CloudSyncSession{ID: "css_1", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", Provider: "mock", ResourceFingerprint: "fp", RequestedBytes: 1, Status: domain.CloudSyncSessionStatusAuthorized, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create sync session: %v", err)
	}
	if err := db.Create(&domain.CloudManifest{ID: "cmf_1", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestHash: "hash", ManifestVersion: 1, ObjectCount: 1, TotalBytes: 1, Status: domain.CloudManifestStatusCommitted, IdempotencyKey: "manifest:1", CreatedAt: now, CommittedAt: &now}).Error; err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	if err := db.Create(&domain.CloudObject{ID: "cob_1", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestID: "cmf_1", ResourceID: "res_1", ResourceKind: "doc", ObjectKey: "obj/1", ContentHash: "hash", SizeBytes: 1, Status: domain.CloudObjectStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}
	if err := db.Create(&domain.CreditAccount{ID: "cred_acc_1", UserID: "usr_1", Balance: 10, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := db.Create(&domain.CreditBucket{ID: "cred_bucket_1", AccountID: "cred_acc_1", UserID: "usr_1", Type: domain.CreditBucketTypeAdmin, Source: "test", Status: domain.CreditBucketStatusActive, IdempotencyKey: "bucket:1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := db.Create(&domain.CreditReservation{ID: "cred_res_1", AccountID: "cred_acc_1", UserID: "usr_1", Operation: "test", Status: domain.CreditReservationStatusPending, IdempotencyKey: "reservation:1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create reservation: %v", err)
	}
	if err := db.Create(&domain.CreditTransaction{ID: "cred_tx_1", AccountID: "cred_acc_1", UserID: "usr_1", Type: domain.CreditTransactionTypeGrant, Amount: 10, IdempotencyKey: "transaction:1", CreatedAt: now}).Error; err != nil {
		t.Fatalf("create transaction: %v", err)
	}
}

func assertAdminResetTableEmpty[T any](t *testing.T, db *gorm.DB) {
	t.Helper()
	var count int64
	if err := db.Model(new(T)).Count(&count).Error; err != nil {
		t.Fatalf("count %T: %v", *new(T), err)
	}
	if count != 0 {
		t.Fatalf("expected %T table empty, count=%d", *new(T), count)
	}
}

func stringPtr(value string) *string {
	return &value
}
