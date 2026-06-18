package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidAccessDevice  = errors.New("invalid access device")
	ErrAccessDeviceNotFound = errors.New("access device not found")
)

type AccessDeviceAdminService interface {
	RevokeDevice(ctx context.Context, input AccessDeviceRevokeInput) (*domain.UserDevice, error)
}

type AccessDeviceRevokeInput struct {
	DeviceID  string
	RevokedBy string
	Reason    string
}

type accessDeviceAdminService struct {
	devices repository.UserDeviceRepository
	now     func() time.Time
}

func NewAccessDeviceAdminService(devices repository.UserDeviceRepository) AccessDeviceAdminService {
	return &accessDeviceAdminService{devices: devices, now: func() time.Time { return time.Now().UTC() }}
}

func (s *accessDeviceAdminService) RevokeDevice(ctx context.Context, input AccessDeviceRevokeInput) (*domain.UserDevice, error) {
	if s == nil || s.devices == nil {
		return nil, ErrInvalidAccessDevice
	}
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.RevokedBy = defaultString(strings.TrimSpace(input.RevokedBy), "admin")
	input.Reason = strings.TrimSpace(input.Reason)
	if input.DeviceID == "" {
		return nil, ErrInvalidAccessDevice
	}
	device, err := s.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrAccessDeviceNotFound
		}
		return nil, err
	}
	now := s.currentTime()
	if device.Status == domain.DeviceStatusDisabled && device.RevokedAt != nil {
		return device, nil
	}
	device.Status = domain.DeviceStatusDisabled
	device.UpdatedAt = now
	device.RevokedAt = &now
	device.RevokedBy = input.RevokedBy
	device.RevokeReason = input.Reason
	if err := s.devices.Update(ctx, device); err != nil {
		return nil, err
	}
	return device, nil
}

func (s *accessDeviceAdminService) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
