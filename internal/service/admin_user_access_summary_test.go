package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAdminUserAccessSummaryReadRepo struct {
	query  repository.AdminUserAccessSummaryQuery
	record *repository.AdminUserAccessSummaryRecord
	err    error
}

func (f *fakeAdminUserAccessSummaryReadRepo) Get(ctx context.Context, query repository.AdminUserAccessSummaryQuery) (*repository.AdminUserAccessSummaryRecord, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return f.record, nil
}

type fakeSoftwareSubscriptionProjector struct {
	userID     string
	projection SoftwareSubscriptionProjection
	err        error
}

func (f *fakeSoftwareSubscriptionProjector) Project(ctx context.Context, userID string) (SoftwareSubscriptionProjection, error) {
	f.userID = userID
	if f.err != nil {
		return SoftwareSubscriptionProjection{}, f.err
	}
	return f.projection, nil
}

func TestAdminUserAccessSummaryServiceProjectsPrivacySafeTroubleshootingView(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	periodEnd := now.Add(30 * 24 * time.Hour)
	trialEnd := now.Add(7 * 24 * time.Hour)
	revokedAt := now.Add(-2 * time.Hour)
	processedAt := now.Add(-time.Minute)
	resolvedAt := now.Add(-30 * time.Minute)
	repo := &fakeAdminUserAccessSummaryReadRepo{record: &repository.AdminUserAccessSummaryRecord{
		User: domain.User{
			ID:          "usr_1",
			Email:       "Writer@Example.COM",
			DisplayName: "Secret Writer",
			Status:      domain.UserStatusActive,
			CreatedAt:   now.Add(-24 * time.Hour),
			UpdatedAt:   now,
		},
		Devices: []domain.UserDevice{
			{ID: "dev_1", UserID: "usr_1", DeviceID: "device-raw-1", Status: domain.DeviceStatusActive, FirstSeenAt: now.Add(-24 * time.Hour), LastSeenAt: now},
			{ID: "dev_2", UserID: "usr_1", DeviceID: "device-raw-2", Status: domain.DeviceStatusDisabled, FirstSeenAt: now.Add(-24 * time.Hour), LastSeenAt: now.Add(-time.Hour), RevokedAt: &revokedAt, RevokedBy: "ops"},
		},
		TrialGrants: []domain.TrialGrant{
			{ID: "trl_1", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, StartsAt: now.Add(-time.Hour), ExpiresAt: &trialEnd, CreatedAt: now.Add(-time.Hour)},
		},
		EntitlementGrants: []domain.EntitlementGrant{
			{ID: "grt_studio", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd, CreatedAt: now.Add(-time.Hour)},
			{ID: "grt_cloud", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd, CreatedAt: now.Add(-time.Hour)},
			{ID: "grt_legacy", UserID: "usr_1", EntitlementID: domain.EntitlementWorkflowAdvanced, Status: domain.GrantStatusActive, StartsAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour)},
		},
		Orders: []domain.Order{
			{OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "creem", Amount: 1200, Currency: "USD", CheckoutURL: "https://checkout.example/secret", ProviderCheckoutID: "ch_secret", Metadata: `{"walnut_provider_subscription_id":"sub_secret"}`, PaidAt: &now, FulfilledAt: &processedAt, OrderType: domain.OrderTypeCheckout},
		},
		PaymentEvents: []domain.PaymentEventInbox{
			{ID: "pev_1", Provider: "creem", ProviderEventID: "evt_secret", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-1", ProviderTradeNo: "trade_secret", Amount: 1200, Currency: "USD", PayloadHash: "hash_123", RawPayload: `{"email":"writer@example.com"}`, Status: domain.PaymentEventStatusProcessed, Attempts: 1, LastError: "notify writer@example.com", ReceivedAt: now.Add(-2 * time.Minute), ProcessedAt: &processedAt},
		},
		RiskFlags: []domain.PaymentRiskFlag{
			{ID: "risk_open", UserID: "usr_1", OutTradeNo: "CHK-1", Reason: domain.PaymentRiskReasonDispute, Severity: domain.PaymentRiskSeverityCritical, Status: domain.PaymentRiskStatusOpen, Note: "contact writer@example.com", CreatedAt: now.Add(-time.Hour)},
			{ID: "risk_resolved", UserID: "usr_1", OutTradeNo: "CHK-0", Reason: domain.PaymentRiskReasonChargeback, Severity: domain.PaymentRiskSeverityHigh, Status: domain.PaymentRiskStatusResolved, Note: "resolved writer@example.com", ResolvedBy: "ops@example.com", CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: &resolvedAt},
		},
		CloudProjects: []domain.CloudProject{
			{ID: "cpj_1", UserID: "usr_1", ClientProjectID: "client-project-1", Name: "Secret Manuscript", Status: domain.CloudProjectStatusActive, LastManifestID: "cmf_1", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		},
		CloudUsedBytes: 200,
	}}
	projector := &fakeSoftwareSubscriptionProjector{projection: SoftwareSubscriptionProjection{
		UserID:               "usr_1",
		SKUCode:              domain.SKUProOwnAIMonthly,
		Status:               SoftwareSubscriptionStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:    true,
		CurrentPeriodEndsAt:  periodEnd.Format(time.RFC3339),
		CurrentPeriodStartAt: now.Add(-time.Hour).Format(time.RFC3339),
	}}
	svc := NewAdminUserAccessSummaryService(AdminUserAccessSummaryDependencies{
		ReadModel:             repo,
		SoftwareSubscriptions: projector,
		CloudQuotaPolicy:      NewStaticCloudStorageQuotaPolicy(1000),
		Privacy:               NewAdminPrivacyProjector(),
		MaxDevices:            3,
		Now:                   func() time.Time { return now },
	})

	result, err := svc.Get(context.Background(), AdminUserAccessSummaryInput{UserID: " usr_1 ", RecentLimit: 1})
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if repo.query.UserID != "usr_1" || repo.query.RecentLimit != 1 || projector.userID != "usr_1" {
		t.Fatalf("expected normalized dependencies, repo=%#v projector_user=%s", repo.query, projector.userID)
	}
	if result.User.EmailMasked != "wr**er@example.com" || result.User.EmailFingerprint == "" || result.User.DisplayNameMasked == "Secret Writer" {
		t.Fatalf("expected privacy-safe user projection, got %#v", result.User)
	}
	if result.Devices.ActiveCount != 1 || result.Devices.RevokedCount != 1 || result.Devices.Capacity.RemainingDeviceSlots != 2 {
		t.Fatalf("unexpected device summary: %#v", result.Devices)
	}
	if len(result.Devices.RecentDeviceRows) != 1 || result.Devices.RecentDeviceRows[0].DeviceIDMasked == "device-raw-1" {
		t.Fatalf("expected limited masked device rows, got %#v", result.Devices.RecentDeviceRows)
	}
	if result.Trial.CurrentStatus != domain.TrialGrantStatusIssued || result.Subscription.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd {
		t.Fatalf("expected trial/subscription summary, got trial=%#v subscription=%#v", result.Trial, result.Subscription)
	}
	if len(result.Grants.Active) != 1 || len(result.Grants.CurrentEntitlements) != 2 || len(result.Grants.LegacyEntitlements) != 1 {
		t.Fatalf("expected grants summary with limited records, got %#v", result.Grants)
	}
	if len(result.Orders) != 1 || !result.Orders[0].HasCheckout || !result.Orders[0].HasMetadata {
		t.Fatalf("expected safe order flags, got %#v", result.Orders)
	}
	if len(result.PaymentEvents) != 1 || result.PaymentEvents[0].PayloadHash != "hash_123" || result.PaymentEvents[0].LastErrorRedacted == "notify writer@example.com" {
		t.Fatalf("expected payment event payload hash and redacted error, got %#v", result.PaymentEvents)
	}
	if result.RiskFlags.OpenCount != 1 || result.RiskFlags.ResolvedCount != 1 || result.RiskFlags.CriticalOpenCount != 1 || len(result.RiskFlags.Recent) != 1 {
		t.Fatalf("expected all risk counts but limited recent rows, got %#v", result.RiskFlags)
	}
	if result.CloudStorage.UsedBytes != 200 || result.CloudStorage.QuotaBytes != 1000 || result.CloudStorage.ActiveProjectCount != 1 {
		t.Fatalf("expected cloud usage summary, got %#v", result.CloudStorage)
	}

	raw, _ := json.Marshal(result)
	body := string(raw)
	for _, leaked := range []string{
		"Writer@Example.COM",
		"writer@example.com",
		"Secret Writer",
		"device-raw-1",
		"device-raw-2",
		"https://checkout.example/secret",
		"ch_secret",
		"sub_secret",
		"evt_secret",
		"trade_secret",
		`{"email":"writer@example.com"}`,
		"Secret Manuscript",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("summary leaked %q in %s", leaked, body)
		}
	}
}

func TestAdminUserAccessSummaryServiceMapsMissingUser(t *testing.T) {
	svc := NewAdminUserAccessSummaryService(AdminUserAccessSummaryDependencies{
		ReadModel: &fakeAdminUserAccessSummaryReadRepo{err: repository.ErrNotFound},
	})

	_, err := svc.Get(context.Background(), AdminUserAccessSummaryInput{UserID: "usr_missing"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected user not found, got %v", err)
	}
}
