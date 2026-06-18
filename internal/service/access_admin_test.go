package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAccessAccountReadRepo struct {
	query   repository.AccessAccountQuery
	records []repository.AccessAccountRecord
	total   int64
}

func (f *fakeAccessAccountReadRepo) List(ctx context.Context, query repository.AccessAccountQuery) ([]repository.AccessAccountRecord, int64, error) {
	f.query = query
	return f.records, f.total, nil
}

func TestAccessAdminServiceMasksEmailAndProjectsCurrentEntitlements(t *testing.T) {
	now := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	expiresAt := now.Add(14 * 24 * time.Hour)
	repo := &fakeAccessAccountReadRepo{
		total: 1,
		records: []repository.AccessAccountRecord{{
			User:        domain.User{ID: "usr_1", Email: "Writer@Example.COM", DisplayName: "Writer", Status: domain.UserStatusActive, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
			Devices:     []domain.UserDevice{{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, LastSeenAt: now}},
			TrialGrants: []domain.TrialGrant{{ID: "trl_1", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, StartsAt: now.Add(-time.Hour), ExpiresAt: &expiresAt, CreatedAt: now.Add(-time.Hour)}},
			EntitlementGrants: []domain.EntitlementGrant{
				{ID: "grt_1", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, StartsAt: now.Add(-time.Hour)},
				{ID: "grt_2", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, StartsAt: now.Add(-time.Hour)},
				{ID: "grt_legacy", UserID: "usr_1", EntitlementID: domain.EntitlementWorkflowAdvanced, Status: domain.GrantStatusActive, StartsAt: now.Add(-time.Hour)},
			},
		}},
	}
	svc := &accessAdminServiceImpl{repo: repo, now: func() time.Time { return now }}

	result, err := svc.ListAccounts(context.Background(), AccessAdminQuery{Email: " Writer@Example.COM ", Limit: 999})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if repo.query.Email != "writer@example.com" || repo.query.Limit != maxAccessAccountLimit {
		t.Fatalf("expected normalized query, got %#v", repo.query)
	}
	if result.Total != 1 || len(result.Accounts) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	account := result.Accounts[0]
	if account.EmailMasked != "wr**er@example.com" || account.EmailDomain != "example.com" || account.EmailFingerprint == "" {
		t.Fatalf("expected masked email projection, got %#v", account)
	}
	if account.DisplayNameMasked != "W***" || account.ActiveDeviceCount != 1 || account.TrialStatus != domain.TrialGrantStatusIssued {
		t.Fatalf("expected account summary, got %#v", account)
	}
	if len(account.Devices) != 1 || account.Devices[0].ID != "dev_1" || account.Devices[0].DeviceIDMasked == "device-1" || account.Devices[0].DeviceIDFingerprint == "" {
		t.Fatalf("expected privacy-safe device projection, got %#v", account.Devices)
	}
	if !reflect.DeepEqual(account.CurrentEntitlements, []string{domain.EntitlementCloudStorage, domain.EntitlementEditorialStudio}) {
		t.Fatalf("expected current entitlements only, got %#v", account.CurrentEntitlements)
	}
	if !reflect.DeepEqual(account.LegacyEntitlements, []string{domain.EntitlementWorkflowAdvanced}) {
		t.Fatalf("expected legacy entitlements, got %#v", account.LegacyEntitlements)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "writer@example.com") || strings.Contains(string(raw), "Writer@Example.COM") || strings.Contains(string(raw), "device-1") {
		t.Fatalf("admin access account response leaked raw email: %s", raw)
	}
}

func TestAccessAdminServiceMarksExpiredTrials(t *testing.T) {
	now := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Hour)
	repo := &fakeAccessAccountReadRepo{records: []repository.AccessAccountRecord{{
		User:        domain.User{ID: "usr_1", Email: "a@example.com"},
		TrialGrants: []domain.TrialGrant{{ID: "trl_1", Status: domain.TrialGrantStatusIssued, ExpiresAt: &expiresAt}},
	}}}
	svc := &accessAdminServiceImpl{repo: repo, now: func() time.Time { return now }}

	result, err := svc.ListAccounts(context.Background(), AccessAdminQuery{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if result.Accounts[0].TrialStatus != "expired" {
		t.Fatalf("expected expired trial, got %#v", result.Accounts[0])
	}
}

func TestAccessDeviceAdminService_RevokeDevice(t *testing.T) {
	now := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	devices := newMockUserDeviceRepo()
	devices.devices["usr_1:device-1"] = &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, LastSeenAt: now.Add(-time.Hour)}
	svc := &accessDeviceAdminService{devices: devices, now: func() time.Time { return now }}

	device, err := svc.RevokeDevice(context.Background(), AccessDeviceRevokeInput{DeviceID: "dev_1", RevokedBy: "ops", Reason: "lost laptop"})
	if err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if device.Status != domain.DeviceStatusDisabled || device.RevokedAt == nil || device.RevokedBy != "ops" || device.RevokeReason != "lost laptop" {
		t.Fatalf("expected revoked device, got %#v", device)
	}

	again, err := svc.RevokeDevice(context.Background(), AccessDeviceRevokeInput{DeviceID: "dev_1", RevokedBy: "ops"})
	if err != nil || again.ID != "dev_1" {
		t.Fatalf("expected idempotent revoke, device=%#v err=%v", again, err)
	}
}

func TestAccessDeviceAdminService_RevokeDeviceNotFound(t *testing.T) {
	svc := NewAccessDeviceAdminService(newMockUserDeviceRepo())
	_, err := svc.RevokeDevice(context.Background(), AccessDeviceRevokeInput{DeviceID: "missing"})
	if !errors.Is(err, ErrAccessDeviceNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
