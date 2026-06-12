package service

import (
	"context"
	"errors"
	"walnut-billing/internal/domain"
	"testing"
	"time"
)

// MockLicenseRepository implements repository.LicenseRepository for testing.
type MockLicenseRepository struct {
	licenses map[string]*domain.License
}

func NewMockLicenseRepository() *MockLicenseRepository {
	return &MockLicenseRepository{
		licenses: make(map[string]*domain.License),
	}
}

func (m *MockLicenseRepository) Create(ctx context.Context, license *domain.License) error {
	m.licenses[license.Key] = license
	return nil
}

func (m *MockLicenseRepository) GetByKey(ctx context.Context, key string) (*domain.License, error) {
	lic, ok := m.licenses[key]
	if !ok {
		return nil, errors.New("record not found")
	}
	return lic, nil
}

func (m *MockLicenseRepository) Update(ctx context.Context, license *domain.License) error {
	m.licenses[license.Key] = license
	return nil
}

func (m *MockLicenseRepository) List(ctx context.Context, status string) ([]domain.License, error) {
	var result []domain.License
	for _, lic := range m.licenses {
		if status == "" || lic.Status == status {
			result = append(result, *lic)
		}
	}
	return result, nil
}

func TestLicenseService_Verify_Success(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0001-0001"] = &domain.License{
		Key:      "SM-PRO-0001-0001",
		Status:   "active",
		DeviceID: "device-1",
	}

	svc := NewLicenseService(repo)
	lic, err := svc.Verify(context.Background(), "SM-PRO-0001-0001", "device-1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if lic.Key != "SM-PRO-0001-0001" {
		t.Errorf("expected key SM-PRO-0001-0001, got %s", lic.Key)
	}
}

func TestLicenseService_Verify_NotFound(t *testing.T) {
	repo := NewMockLicenseRepository()
	svc := NewLicenseService(repo)

	_, err := svc.Verify(context.Background(), "nonexistent", "device-1")
	if err != ErrLicenseNotFound {
		t.Errorf("expected ErrLicenseNotFound, got: %v", err)
	}
}

func TestLicenseService_Verify_Inactive(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0002-0002"] = &domain.License{
		Key:    "SM-PRO-0002-0002",
		Status: "inactive",
	}
	svc := NewLicenseService(repo)

	_, err := svc.Verify(context.Background(), "SM-PRO-0002-0002", "device-1")
	if err != ErrLicenseInactive {
		t.Errorf("expected ErrLicenseInactive, got: %v", err)
	}
}

func TestLicenseService_Verify_Expired(t *testing.T) {
	repo := NewMockLicenseRepository()
	expired := time.Now().Add(-1 * time.Hour)
	repo.licenses["SM-PRO-0003-0003"] = &domain.License{
		Key:       "SM-PRO-0003-0003",
		Status:    "active",
		ExpiresAt: &expired,
	}
	svc := NewLicenseService(repo)

	_, err := svc.Verify(context.Background(), "SM-PRO-0003-0003", "device-1")
	if err != ErrLicenseExpired {
		t.Errorf("expected ErrLicenseExpired, got: %v", err)
	}
}

func TestLicenseService_Verify_DeviceBound(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0004-0004"] = &domain.License{
		Key:      "SM-PRO-0004-0004",
		Status:   "active",
		DeviceID: "device-1",
	}
	svc := NewLicenseService(repo)

	_, err := svc.Verify(context.Background(), "SM-PRO-0004-0004", "device-2")
	if err != ErrDeviceBound {
		t.Errorf("expected ErrDeviceBound, got: %v", err)
	}
}

func TestLicenseService_Activate_Success(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0005-0005"] = &domain.License{
		Key:    "SM-PRO-0005-0005",
		Status: "inactive",
	}
	svc := NewLicenseService(repo)

	err := svc.Activate(context.Background(), "SM-PRO-0005-0005", "device-1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lic, _ := repo.GetByKey(context.Background(), "SM-PRO-0005-0005")
	if lic.Status != "active" {
		t.Errorf("expected status 'active', got %s", lic.Status)
	}
	if lic.DeviceID != "device-1" {
		t.Errorf("expected DeviceID 'device-1', got %s", lic.DeviceID)
	}
}

func TestLicenseService_Activate_Idempotent(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0006-0006"] = &domain.License{
		Key:      "SM-PRO-0006-0006",
		Status:   "active",
		DeviceID: "device-1",
	}
	svc := NewLicenseService(repo)

	// Activating again on same device should be idempotent
	err := svc.Activate(context.Background(), "SM-PRO-0006-0006", "device-1")
	if err != nil {
		t.Fatalf("expected no error for idempotent activate, got: %v", err)
	}
}

func TestLicenseService_Activate_DeviceBound(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0007-0007"] = &domain.License{
		Key:      "SM-PRO-0007-0007",
		Status:   "active",
		DeviceID: "device-1",
	}
	svc := NewLicenseService(repo)

	err := svc.Activate(context.Background(), "SM-PRO-0007-0007", "device-2")
	// Single-seat license: returns ErrSeatsExhausted when trying to add another device
	if err != ErrSeatsExhausted {
		t.Errorf("expected ErrSeatsExhausted, got: %v", err)
	}
}

func TestLicenseService_Deactivate_Success(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0008-0008"] = &domain.License{
		Key:      "SM-PRO-0008-0008",
		Status:   "active",
		DeviceID: "device-1",
	}
	svc := NewLicenseService(repo)

	err := svc.Deactivate(context.Background(), "SM-PRO-0008-0008", "device-1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	lic, _ := repo.GetByKey(context.Background(), "SM-PRO-0008-0008")
	if lic.Status != "inactive" {
		t.Errorf("expected status 'inactive', got %s", lic.Status)
	}
	if lic.DeviceID != "" {
		t.Errorf("expected empty DeviceID, got %s", lic.DeviceID)
	}
}

func TestLicenseService_Deactivate_DeviceNotBound(t *testing.T) {
	repo := NewMockLicenseRepository()
	repo.licenses["SM-PRO-0009-0009"] = &domain.License{
		Key:      "SM-PRO-0009-0009",
		Status:   "active",
		DeviceID: "device-1",
	}
	svc := NewLicenseService(repo)

	err := svc.Deactivate(context.Background(), "SM-PRO-0009-0009", "device-2")
	if err != ErrDeviceNotBound {
		t.Errorf("expected ErrDeviceNotBound, got: %v", err)
	}
}

func TestLicenseService_CheckExpiry(t *testing.T) {
	repo := NewMockLicenseRepository()
	expired := time.Now().Add(-1 * time.Hour)
	valid := time.Now().Add(1 * time.Hour)

	repo.licenses["SM-SUB-0001-0001"] = &domain.License{
		Key:       "SM-SUB-0001-0001",
		Status:    "active",
		ExpiresAt: &expired,
	}
	repo.licenses["SM-SUB-0002-0002"] = &domain.License{
		Key:       "SM-SUB-0002-0002",
		Status:    "active",
		ExpiresAt: &valid,
	}

	svc := NewLicenseService(repo)
	count, err := svc.CheckExpiry(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired license, got %d", count)
	}

	lic, _ := repo.GetByKey(context.Background(), "SM-SUB-0001-0001")
	if lic.Status != "expired" {
		t.Errorf("expected status 'expired', got %s", lic.Status)
	}

	lic2, _ := repo.GetByKey(context.Background(), "SM-SUB-0002-0002")
	if lic2.Status != "active" {
		t.Errorf("expected status 'active', got %s", lic2.Status)
	}
}

func TestLicenseService_GetLicenseStatus_WithExpiry(t *testing.T) {
	repo := NewMockLicenseRepository()
	exp := time.Now().AddDate(0, 0, 15)
	repo.licenses["SM-SUB-0010-0010"] = &domain.License{
		Key:       "SM-SUB-0010-0010",
		Status:    "active",
		Validity:  "monthly",
		ExpiresAt: &exp,
		MaxSeats:  1,
	}

	svc := NewLicenseService(repo)
	info, err := svc.GetLicenseStatus(context.Background(), "SM-SUB-0010-0010")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.IsExpired {
		t.Error("expected not expired")
	}
	if info.DaysRemaining <= 0 {
		t.Errorf("expected positive days remaining, got %d", info.DaysRemaining)
	}
}

func TestLicenseService_GetLicenseStatus_GracePeriod(t *testing.T) {
	repo := NewMockLicenseRepository()
	// Expired 1 day ago (within grace period)
	exp := time.Now().Add(-24 * time.Hour)
	repo.licenses["SM-SUB-0011-0011"] = &domain.License{
		Key:       "SM-SUB-0011-0011",
		Status:    "active",
		Validity:  "monthly",
		ExpiresAt: &exp,
	}

	svc := NewLicenseService(repo)
	info, err := svc.GetLicenseStatus(context.Background(), "SM-SUB-0011-0011")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsExpired {
		t.Error("expected expired flag")
	}
	if !info.IsInGrace {
		t.Error("expected grace period flag")
	}
	if info.Status != "grace" {
		t.Errorf("expected status 'grace', got %s", info.Status)
	}
}

func TestLicenseService_GetLicenseStatus_ExpiredBeyondGrace(t *testing.T) {
	repo := NewMockLicenseRepository()
	// Expired 10 days ago (beyond grace period)
	exp := time.Now().AddDate(0, 0, -10)
	repo.licenses["SM-SUB-0012-0012"] = &domain.License{
		Key:       "SM-SUB-0012-0012",
		Status:    "active",
		Validity:  "monthly",
		ExpiresAt: &exp,
	}

	svc := NewLicenseService(repo)
	info, err := svc.GetLicenseStatus(context.Background(), "SM-SUB-0012-0012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsExpired {
		t.Error("expected expired flag")
	}
	if info.IsInGrace {
		t.Error("should NOT be in grace period")
	}
}

func TestLicenseService_ListExpiringSoon(t *testing.T) {
	repo := NewMockLicenseRepository()
	tomorrow := time.Now().AddDate(0, 0, 1)
	nextWeek := time.Now().AddDate(0, 0, 7)
	nextMonth := time.Now().AddDate(0, 1, 0)

	repo.licenses["SM-SUB-0020-0020"] = &domain.License{
		Key: "SM-SUB-0020-0020", Status: "active", Validity: "monthly", ExpiresAt: &tomorrow,
	}
	repo.licenses["SM-SUB-0021-0021"] = &domain.License{
		Key: "SM-SUB-0021-0021", Status: "active", Validity: "monthly", ExpiresAt: &nextWeek,
	}
	repo.licenses["SM-SUB-0022-0022"] = &domain.License{
		Key: "SM-SUB-0022-0022", Status: "active", Validity: "monthly", ExpiresAt: &nextMonth,
	}

	svc := NewLicenseService(repo)

	// Expiring within 5 days
	expiring, err := svc.ListExpiringSoon(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expiring) != 1 {
		t.Errorf("expected 1 license expiring within 5 days, got %d", len(expiring))
	}

	// Expiring within 10 days
	expiring, err = svc.ListExpiringSoon(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expiring) != 2 {
		t.Errorf("expected 2 licenses expiring within 10 days, got %d", len(expiring))
	}
}
