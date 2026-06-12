package service

import (
	"context"
	"errors"
	"fmt"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/metrics"
	"walnut-billing/internal/repository"
	"strings"
	"time"
)

var (
	ErrLicenseNotFound    = errors.New("license not found")
	ErrLicenseInactive    = errors.New("license is inactive")
	ErrLicenseExpired     = errors.New("license has expired")
	ErrDeviceBound        = errors.New("license is bound to another device")
	ErrSeatsExhausted     = errors.New("all seats are already bound to other devices")
	ErrDeviceNotBound     = errors.New("device is not bound to this license")
)

// LicenseService handles license lifecycle management.
type LicenseService interface {
	Verify(ctx context.Context, key, deviceID string) (*domain.License, error)
	Activate(ctx context.Context, key, deviceID string) error
	Deactivate(ctx context.Context, key, deviceID string) error
	ActivateByKey(ctx context.Context, key string) error
	GetLicenseByKey(ctx context.Context, key string) (*domain.License, error)
	ListLicenses(ctx context.Context, status string) ([]domain.License, error)
	CheckExpiry(ctx context.Context) (int, error)
	GetLicenseStatus(ctx context.Context, key string) (LicenseStatusInfo, error)
	ListExpiringSoon(ctx context.Context, days int) ([]LicenseStatusInfo, error)
}

// LicenseStatusInfo provides detailed status including grace period.
type LicenseStatusInfo struct {
	Key           string     `json:"key"`
	Status        string     `json:"status"`
	Validity      string     `json:"validity"`
	DeviceID      string     `json:"device_id"`
	ActivatedAt   *time.Time `json:"activated_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	MaxSeats      int        `json:"max_seats"`
	IsExpired     bool       `json:"is_expired"`
	IsInGrace     bool       `json:"is_in_grace"`
	DaysRemaining int        `json:"days_remaining"` // Negative if expired
}

type licenseServiceImpl struct {
	repo repository.LicenseRepository
}

func NewLicenseService(repo repository.LicenseRepository) LicenseService {
	return &licenseServiceImpl{repo: repo}
}

func (s *licenseServiceImpl) Verify(ctx context.Context, key, deviceID string) (*domain.License, error) {
	lic, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		return nil, ErrLicenseNotFound
	}

	if lic.Status != "active" {
		return nil, ErrLicenseInactive
	}

	if lic.ExpiresAt != nil && lic.ExpiresAt.Before(time.Now()) {
		return nil, ErrLicenseExpired
	}

	// Multi-seat: check if device is bound
	if !s.isDeviceBound(lic, deviceID) {
		return nil, ErrDeviceBound
	}

	return lic, nil
}

func (s *licenseServiceImpl) Activate(ctx context.Context, key, deviceID string) error {
	lic, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		return ErrLicenseNotFound
	}

	if lic.Status != "inactive" && lic.Status != "active" {
		return fmt.Errorf("license is %s", lic.Status)
	}

	devices := s.parseDeviceIDs(lic)

	// Already bound to this device — idempotent
	if s.containsDevice(devices, deviceID) {
		return nil
	}

	// Check seat limit
	maxSeats := lic.MaxSeats
	if maxSeats <= 0 {
		maxSeats = 1
	}
	if len(devices) >= maxSeats {
		return ErrSeatsExhausted
	}

	// Add device
	devices = append(devices, deviceID)
	lic.DeviceID = strings.Join(devices, ",")
	now := time.Now()
	lic.ActivatedAt = &now
	lic.Status = "active"

	if err := s.repo.Update(ctx, lic); err != nil {
		return err
	}

	metrics.LicenseActivationsTotal.Inc()
	return nil
}

func (s *licenseServiceImpl) Deactivate(ctx context.Context, key, deviceID string) error {
	lic, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		return ErrLicenseNotFound
	}

	if lic.Status != "active" {
		return fmt.Errorf("license is %s", lic.Status)
	}

	devices := s.parseDeviceIDs(lic)

	// Find and remove the device
	var newDevices []string
	found := false
	for _, d := range devices {
		if d == deviceID {
			found = true
		} else {
			newDevices = append(newDevices, d)
		}
	}
	if !found {
		return ErrDeviceNotBound
	}

	lic.DeviceID = strings.Join(newDevices, ",")

	// If no devices remain, deactivate
	if len(newDevices) == 0 {
		lic.Status = "inactive"
		lic.DeviceID = ""
	}

	return s.repo.Update(ctx, lic)
}

func (s *licenseServiceImpl) ActivateByKey(ctx context.Context, key string) error {
	lic, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		return ErrLicenseNotFound
	}

	if lic.Status == "active" {
		return nil
	}

	lic.Status = "active"
	return s.repo.Update(ctx, lic)
}

func (s *licenseServiceImpl) GetLicenseByKey(ctx context.Context, key string) (*domain.License, error) {
	return s.repo.GetByKey(ctx, key)
}

func (s *licenseServiceImpl) ListLicenses(ctx context.Context, status string) ([]domain.License, error) {
	return s.repo.List(ctx, status)
}

// CheckExpiry deactivates all expired licenses. Returns count of deactivated.
func (s *licenseServiceImpl) CheckExpiry(ctx context.Context) (int, error) {
	allLicenses, err := s.repo.List(ctx, "active")
	if err != nil {
		return 0, err
	}

	count := 0
	for _, lic := range allLicenses {
		if lic.ExpiresAt != nil && lic.ExpiresAt.Before(time.Now()) {
			lic.Status = "expired"
			if err := s.repo.Update(ctx, &lic); err != nil {
				// Log error but continue
				continue
			}
			count++
		}
	}
	return count, nil
}

// parseDeviceIDs returns the list of bound device IDs.
func (s *licenseServiceImpl) parseDeviceIDs(lic *domain.License) []string {
	if lic.DeviceID == "" {
		return nil
	}
	return strings.Split(lic.DeviceID, ",")
}

func (s *licenseServiceImpl) containsDevice(devices []string, deviceID string) bool {
	for _, d := range devices {
		if d == deviceID {
			return true
		}
	}
	return false
}

func (s *licenseServiceImpl) isDeviceBound(lic *domain.License, deviceID string) bool {
	devices := s.parseDeviceIDs(lic)
	return s.containsDevice(devices, deviceID)
}

func (s *licenseServiceImpl) GetLicenseStatus(ctx context.Context, key string) (LicenseStatusInfo, error) {
	lic, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		return LicenseStatusInfo{}, ErrLicenseNotFound
	}
	return s.toStatusInfo(lic), nil
}

func (s *licenseServiceImpl) ListExpiringSoon(ctx context.Context, days int) ([]LicenseStatusInfo, error) {
	allLicenses, err := s.repo.List(ctx, "active")
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().AddDate(0, 0, days)
	var expiring []LicenseStatusInfo
	for _, lic := range allLicenses {
		if lic.ExpiresAt != nil && lic.ExpiresAt.Before(cutoff) {
			expiring = append(expiring, s.toStatusInfo(&lic))
		}
	}
	return expiring, nil
}

func (s *licenseServiceImpl) toStatusInfo(lic *domain.License) LicenseStatusInfo {
	info := LicenseStatusInfo{
		Key:       lic.Key,
		Status:    lic.Status,
		Validity:  lic.Validity,
		DeviceID:  lic.DeviceID,
		ActivatedAt: lic.ActivatedAt,
		ExpiresAt: lic.ExpiresAt,
		MaxSeats:  lic.MaxSeats,
	}

	if lic.ExpiresAt != nil {
		now := time.Now()
		diff := lic.ExpiresAt.Sub(now)
		info.DaysRemaining = int(diff.Hours() / 24)

		if diff < 0 {
			info.IsExpired = true
			// Grace period: within N days after expiry
			graceCutoff := now.AddDate(0, 0, -domain.GracePeriodDays)
			if lic.ExpiresAt.After(graceCutoff) {
				info.IsInGrace = true
				info.Status = "grace"
			}
		}
	}

	return info
}
