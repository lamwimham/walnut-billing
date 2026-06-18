package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
)

func newAccessSnapshotTestIssuer(policy AccessSnapshotPolicy, signer AccessSnapshotSigner) (AccessSnapshotIssuer, *mockEntitlementUserRepo, *mockUserDeviceRepo, *mockTrialGrantRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockSubscriptionCancellationRepo, AccessSnapshotSigner) {
	users := newMockEntitlementUserRepo()
	devices := newMockUserDeviceRepo()
	trials := newMockTrialGrantRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	if signer == nil {
		signer, _ = NewHMACAccessSnapshotSigner("test-secret", "test-key")
	}
	return NewAccessSnapshotIssuer(AccessSnapshotIssuerDependencies{
		Repositories: AccessSnapshotIssuerRepositories{
			Users:             users,
			Devices:           devices,
			TrialGrants:       trials,
			EntitlementGrants: grants,
			CreditAccounts:    accounts,
			Cancellations:     cancellations,
		},
		Policy: policy,
		Signer: signer,
	}), users, devices, trials, grants, accounts, cancellations, signer
}

func TestAccessSnapshotIssuer_IssuesSignedTrialSnapshot(t *testing.T) {
	policy := NewConfigurableAccessSnapshotPolicy(AccessSnapshotPolicyConfig{TTLSeconds: 60, OfflineGraceSeconds: 120, MaxDevices: 2, CloudStorageQuotaMB: 2048})
	issuer, users, devices, trials, grants, _, _, signer := newAccessSnapshotTestIssuer(policy, nil)
	now := time.Now().UTC()
	expires := now.AddDate(0, 0, 14)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", DisplayName: "Writer", Status: domain.UserStatusActive}
	devices.devices["usr_1:device-1"] = &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive}
	devices.devices["usr_1:device-2"] = &domain.UserDevice{ID: "dev_2", UserID: "usr_1", DeviceID: "device-2", Status: domain.DeviceStatusActive}
	trials.grants[trialGrantIdempotencyKey("writer@example.com", domain.TrialGrantTypeProOwnAI)] = &domain.TrialGrant{
		ID:             "trl_1",
		UserID:         "usr_1",
		Email:          "writer@example.com",
		GrantType:      domain.TrialGrantTypeProOwnAI,
		Status:         domain.TrialGrantStatusIssued,
		StartsAt:       now.Add(-time.Minute),
		ExpiresAt:      &expires,
		IdempotencyKey: trialGrantIdempotencyKey("writer@example.com", domain.TrialGrantTypeProOwnAI),
	}
	for _, entitlementID := range CurrentAdvancedEntitlements() {
		key := trialEntitlementGrantKey("trial", entitlementID)
		grants.grants[key] = &domain.EntitlementGrant{ID: key, UserID: "usr_1", EntitlementID: entitlementID, Status: domain.GrantStatusActive, Source: domain.GrantSourceTrial, StartsAt: now.Add(-time.Minute), ExpiresAt: &expires}
	}

	snapshot, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("issue snapshot: %v", err)
	}
	if snapshot.Version != 2 || snapshot.Signature == "" || snapshot.SignatureKeyID != "test-key" || snapshot.SignatureAlg != "HS256" {
		t.Fatalf("expected signed v2 snapshot, got %#v", snapshot)
	}
	if err := signer.Verify(*snapshot); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
	if snapshot.License.State != AccessLicenseStateTrial || snapshot.License.Plan != AccessPlanProOwnAITrial || snapshot.License.TrialEndsAt == "" {
		t.Fatalf("expected trial license projection, got %#v", snapshot.License)
	}
	if snapshot.Device.ID != "dev_1" || snapshot.Device.MaxDevices != 2 || snapshot.Device.ActiveDeviceCount != 2 || snapshot.Device.RemainingDeviceSlots != 0 {
		t.Fatalf("expected device projection, got %#v", snapshot.Device)
	}
	if quota, ok := snapshot.Features["cloud.storage.quota_mb"].(int64); !ok || quota != 2048 {
		t.Fatalf("expected cloud quota projection, got %#v", snapshot.Features)
	}
	issuedAt, _ := time.Parse(time.RFC3339, snapshot.IssuedAt)
	expiresAt, _ := time.Parse(time.RFC3339, snapshot.ExpiresAt)
	graceUntil, _ := time.Parse(time.RFC3339, snapshot.OfflineGraceUntil)
	if expiresAt.Sub(issuedAt) != time.Minute || graceUntil.Sub(expiresAt) != 2*time.Minute {
		t.Fatalf("unexpected ttl/grace issued=%s expires=%s grace=%s", snapshot.IssuedAt, snapshot.ExpiresAt, snapshot.OfflineGraceUntil)
	}
}

func TestAccessSnapshotSignerDetectsTampering(t *testing.T) {
	signer, err := NewHMACAccessSnapshotSigner("test-secret", "test")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	snapshot := domain.AccessSnapshotV2{
		Version:      2,
		User:         domain.AccessSnapshotUserV2{ID: "usr_1", Email: "writer@example.com"},
		License:      domain.AccessSnapshotLicenseV2{State: AccessLicenseStateBasic, Plan: domain.PlanBasicOwnAI, AIMode: AccessAIModeBYOK},
		Entitlements: map[string]bool{},
		Features:     map[string]any{},
		Credits:      map[string]int64{},
		IssuedAt:     time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		Source:       "billing_provider",
	}
	sig, err := signer.Sign(snapshot)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	snapshot.Signature = sig
	if err := signer.Verify(snapshot); err != nil {
		t.Fatalf("verify original: %v", err)
	}
	snapshot.Entitlements[domain.EntitlementEditorialStudio] = true
	if !errors.Is(signer.Verify(snapshot), ErrSnapshotSignature) {
		t.Fatalf("expected tampered signature failure")
	}
}

func TestEd25519AccessSnapshotSignerDetectsTampering(t *testing.T) {
	privateKey, _, err := GenerateEd25519AccessSnapshotKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := NewEd25519AccessSnapshotSigner(privateKey, "prod-key")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	snapshot := domain.AccessSnapshotV2{
		Version:        2,
		User:           domain.AccessSnapshotUserV2{ID: "usr_1", Email: "writer@example.com"},
		License:        domain.AccessSnapshotLicenseV2{State: AccessLicenseStateBasic, Plan: domain.PlanBasicOwnAI, AIMode: AccessAIModeBYOK},
		Entitlements:   map[string]bool{},
		Features:       map[string]any{},
		Credits:        map[string]int64{},
		IssuedAt:       time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:      time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		Source:         "billing_provider",
		SignatureKeyID: signer.KeyID(),
		SignatureAlg:   signer.Algorithm(),
	}
	sig, err := signer.Sign(snapshot)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	snapshot.Signature = sig
	if snapshot.SignatureAlg != "Ed25519" || snapshot.SignatureKeyID != "prod-key" {
		t.Fatalf("expected Ed25519 metadata, got %#v", snapshot)
	}
	if err := signer.Verify(snapshot); err != nil {
		t.Fatalf("verify original: %v", err)
	}
	snapshot.Entitlements[domain.EntitlementEditorialStudio] = true
	if !errors.Is(signer.Verify(snapshot), ErrSnapshotSignature) {
		t.Fatalf("expected tampered signature failure")
	}
}

func TestAccessSnapshotIssuer_PaidAccessOverridesTrial(t *testing.T) {
	issuer, users, _, trials, grants, _, _, _ := newAccessSnapshotTestIssuer(nil, nil)
	now := time.Now().UTC()
	trialExpires := now.AddDate(0, 0, 14)
	paidExpires := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	trials.grants["trial"] = &domain.TrialGrant{ID: "trial", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, StartsAt: now.Add(-time.Hour), ExpiresAt: &trialExpires, IdempotencyKey: "trial"}
	grants.grants["trial"] = &domain.EntitlementGrant{ID: "trial", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceTrial, StartsAt: now.Add(-time.Hour), ExpiresAt: &trialExpires}
	grants.grants["paid"] = &domain.EntitlementGrant{ID: "paid", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &paidExpires}

	snapshot, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1"})
	if err != nil {
		t.Fatalf("issue snapshot: %v", err)
	}
	if snapshot.License.State != AccessLicenseStateSubscription || snapshot.License.Plan != domain.SKUProOwnAIMonthly || snapshot.License.SubscriptionEndsAt == "" {
		t.Fatalf("expected paid subscription to override trial, got %#v", snapshot.License)
	}
}

func TestAccessSnapshotIssuer_ProjectsCancelAtPeriodEnd(t *testing.T) {
	issuer, users, _, _, grants, _, cancellations, _ := newAccessSnapshotTestIssuer(nil, nil)
	now := time.Now().UTC()
	paidExpires := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	grants.grants["paid"] = &domain.EntitlementGrant{ID: "paid", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &paidExpires}
	cancellations.cancellations["cancel-1"] = &domain.SubscriptionCancellation{
		ID:                  "sub_cancel_1",
		UserID:              "usr_1",
		SKUCode:             domain.SKUProOwnAIMonthly,
		Status:              SubscriptionCancellationStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: paidExpires,
		IdempotencyKey:      "cancel-1",
	}

	snapshot, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1"})
	if err != nil {
		t.Fatalf("issue snapshot: %v", err)
	}
	if snapshot.License.State != AccessLicenseStateSubscription || snapshot.License.SubscriptionStatus != SubscriptionCancellationStatusCancelAtPeriodEnd || !snapshot.License.CancelAtPeriodEnd {
		t.Fatalf("expected cancel-at-period-end projection, got %#v", snapshot.License)
	}
}

func TestAccessSnapshotIssuer_LifetimeProjection(t *testing.T) {
	issuer, users, _, _, grants, _, _, _ := newAccessSnapshotTestIssuer(nil, nil)
	now := time.Now().UTC()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	grants.grants["lifetime"] = &domain.EntitlementGrant{ID: "lifetime", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour)}

	snapshot, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1"})
	if err != nil {
		t.Fatalf("issue snapshot: %v", err)
	}
	if snapshot.License.State != AccessLicenseStateLifetime || snapshot.License.Plan != domain.SKUProOwnAILifetime {
		t.Fatalf("expected lifetime projection, got %#v", snapshot.License)
	}
}

func TestAccessSnapshotPolicy_NormalizesConfig(t *testing.T) {
	policy := NewConfigurableAccessSnapshotPolicy(AccessSnapshotPolicyConfig{TTLSeconds: -1, OfflineGraceSeconds: -1, MaxDevices: -1, CloudStorageQuotaMB: -1})
	user := &domain.User{ID: "usr_1"}
	if policy.TTL(context.Background(), user) != 24*time.Hour || policy.OfflineGrace(context.Background(), user) != 7*24*time.Hour || policy.MaxDevices(context.Background(), user) != 2 || policy.CloudStorageQuotaMB(context.Background(), user) != 1024 {
		t.Fatalf("expected default normalized access snapshot policy")
	}
}

func TestAccessSnapshotIssuer_RejectsRevokedDevice(t *testing.T) {
	issuer, users, devices, _, _, _, _, _ := newAccessSnapshotTestIssuer(nil, nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	devices.devices["usr_1:device-1"] = &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusDisabled}

	_, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessDeviceRevoked) {
		t.Fatalf("expected revoked device error, got %v", err)
	}
}

func TestAccessSnapshotIssuer_ProjectsDeviceCapacityForUnknownDevice(t *testing.T) {
	policy := NewConfigurableAccessSnapshotPolicy(AccessSnapshotPolicyConfig{MaxDevices: 3})
	issuer, users, devices, _, _, _, _, _ := newAccessSnapshotTestIssuer(policy, nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	devices.devices["usr_1:device-1"] = &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive}

	snapshot, err := issuer.Issue(context.Background(), AccessSnapshotIssueInput{UserID: "usr_1", DeviceID: "unknown-device"})
	if err != nil {
		t.Fatalf("issue snapshot: %v", err)
	}
	if snapshot.Device.DeviceID != "unknown-device" || snapshot.Device.Status != "unknown" {
		t.Fatalf("expected unknown device projection, got %#v", snapshot.Device)
	}
	if snapshot.Device.ActiveDeviceCount != 1 || snapshot.Device.MaxDevices != 3 || snapshot.Device.RemainingDeviceSlots != 2 {
		t.Fatalf("expected capacity projection for unknown device, got %#v", snapshot.Device)
	}
}
