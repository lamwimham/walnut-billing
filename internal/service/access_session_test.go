package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockUserDeviceRepo struct {
	devices map[string]*domain.UserDevice
}

func newMockUserDeviceRepo() *mockUserDeviceRepo {
	return &mockUserDeviceRepo{devices: make(map[string]*domain.UserDevice)}
}

func (m *mockUserDeviceRepo) Create(ctx context.Context, device *domain.UserDevice) error {
	m.devices[device.UserID+":"+device.DeviceID] = device
	return nil
}

func (m *mockUserDeviceRepo) GetByID(ctx context.Context, id string) (*domain.UserDevice, error) {
	for _, device := range m.devices {
		if device.ID == id {
			return device, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockUserDeviceRepo) GetByUserAndDevice(ctx context.Context, userID string, deviceID string) (*domain.UserDevice, error) {
	device, ok := m.devices[userID+":"+deviceID]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return device, nil
}

func (m *mockUserDeviceRepo) ListByUser(ctx context.Context, userID string, status string) ([]domain.UserDevice, error) {
	var result []domain.UserDevice
	for _, device := range m.devices {
		if device.UserID != userID {
			continue
		}
		if status != "" && device.Status != status {
			continue
		}
		result = append(result, *device)
	}
	return result, nil
}

func (m *mockUserDeviceRepo) Update(ctx context.Context, device *domain.UserDevice) error {
	m.devices[device.UserID+":"+device.DeviceID] = device
	return nil
}

type mockTrialGrantRepo struct {
	grants map[string]*domain.TrialGrant
}

func newMockTrialGrantRepo() *mockTrialGrantRepo {
	return &mockTrialGrantRepo{grants: make(map[string]*domain.TrialGrant)}
}

func (m *mockTrialGrantRepo) Create(ctx context.Context, grant *domain.TrialGrant) error {
	m.grants[grant.IdempotencyKey] = grant
	return nil
}

func (m *mockTrialGrantRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.TrialGrant, error) {
	grant, ok := m.grants[key]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return grant, nil
}

func (m *mockTrialGrantRepo) List(ctx context.Context, query repository.TrialGrantQuery) ([]domain.TrialGrant, error) {
	var result []domain.TrialGrant
	for _, grant := range m.grants {
		if query.UserID != "" && grant.UserID != query.UserID {
			continue
		}
		if query.Email != "" && grant.Email != query.Email {
			continue
		}
		if query.GrantType != "" && grant.GrantType != query.GrantType {
			continue
		}
		if query.Status != "" && grant.Status != query.Status {
			continue
		}
		result = append(result, *grant)
	}
	return result, nil
}

func (m *mockTrialGrantRepo) Update(ctx context.Context, grant *domain.TrialGrant) error {
	m.grants[grant.IdempotencyKey] = grant
	return nil
}

func newAccessSessionTestService(policy AccessSessionPolicy) (AccessSessionService, *mockEntitlementUserRepo, *mockUserDeviceRepo, *mockTrialGrantRepo, *mockGrantRepo, *mockCreditAccountRepo) {
	users := newMockEntitlementUserRepo()
	devices := newMockUserDeviceRepo()
	trials := newMockTrialGrantRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	return NewAccessSessionService(AccessSessionDependencies{
		Repositories: AccessSessionRepositories{
			Users:             users,
			Devices:           devices,
			TrialGrants:       trials,
			EntitlementGrants: grants,
			CreditAccounts:    accounts,
		},
		Policy:             policy,
		EntitlementCatalog: DefaultEntitlementCatalog(),
		SnapshotIssuer: NewAccessSnapshotIssuer(AccessSnapshotIssuerDependencies{
			Repositories: AccessSnapshotIssuerRepositories{
				Users:             users,
				Devices:           devices,
				TrialGrants:       trials,
				EntitlementGrants: grants,
				CreditAccounts:    accounts,
			},
			Signer: DefaultAccessSnapshotSigner(),
		}),
	}), users, devices, trials, grants, accounts
}

func TestAccessSessionService_RegisterOrRestoreFirstEmailCreatesTrialSnapshot(t *testing.T) {
	svc, users, devices, trials, grants, _ := newAccessSessionTestService(nil)

	result, err := svc.RegisterOrRestore(context.Background(), AccessSessionInput{
		Email:       " Writer@Example.COM ",
		DisplayName: "Writer",
		DeviceID:    "device-1",
		Source:      "desktop",
	})
	if err != nil {
		t.Fatalf("expected access session, got %v", err)
	}
	if result.User.Email != "writer@example.com" || result.User.DisplayName != "Writer" {
		t.Fatalf("expected normalized user, got %#v", result.User)
	}
	if result.Device == nil || result.Device.DeviceID != "device-1" || result.Device.Status != domain.DeviceStatusActive {
		t.Fatalf("expected active device binding, got %#v", result.Device)
	}
	if !result.TrialCreated || result.Trial == nil || result.Trial.GrantType != domain.TrialGrantTypeProOwnAI || result.Trial.ExpiresAt == nil {
		t.Fatalf("expected new pro trial, got %#v", result)
	}
	if result.AccessSnapshot == nil || result.AccessSnapshot.Version != 2 || result.AccessSnapshot.Signature == "" || result.AccessSnapshot.License.State != AccessLicenseStateTrial {
		t.Fatalf("expected signed trial access snapshot, got %#v", result.AccessSnapshot)
	}
	for _, entitlementID := range CurrentAdvancedEntitlements() {
		if !result.Snapshot.Entitlements[entitlementID] {
			t.Fatalf("expected snapshot entitlement %s, got %#v", entitlementID, result.Snapshot.Entitlements)
		}
	}
	if len(users.users) != 1 || len(devices.devices) != 1 || len(trials.grants) != 1 || len(grants.grants) != len(CurrentAdvancedEntitlements()) {
		t.Fatalf("unexpected side effects users=%d devices=%d trials=%d grants=%d", len(users.users), len(devices.devices), len(trials.grants), len(grants.grants))
	}
}

func TestAccessSessionService_RegisterOrRestoreIsIdempotentForSameEmail(t *testing.T) {
	svc, _, devices, trials, grants, _ := newAccessSessionTestService(nil)
	input := AccessSessionInput{Email: "writer@example.com", DeviceID: "device-1"}

	first, err := svc.RegisterOrRestore(context.Background(), input)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	firstSeen := first.Device.LastSeenAt
	time.Sleep(time.Millisecond)
	second, err := svc.RegisterOrRestore(context.Background(), input)
	if err != nil {
		t.Fatalf("restore register: %v", err)
	}
	if second.TrialCreated {
		t.Fatalf("restore must not create a second trial")
	}
	if first.User.ID != second.User.ID || first.Trial.ID != second.Trial.ID || first.Device.ID != second.Device.ID {
		t.Fatalf("expected same user/trial/device, first=%#v second=%#v", first, second)
	}
	if !second.Device.LastSeenAt.After(firstSeen) {
		t.Fatalf("expected existing device last_seen refresh")
	}
	if len(devices.devices) != 1 || len(trials.grants) != 1 || len(grants.grants) != len(CurrentAdvancedEntitlements()) {
		t.Fatalf("expected idempotent restore side effects, devices=%d trials=%d grants=%d", len(devices.devices), len(trials.grants), len(grants.grants))
	}
}

func TestAccessSessionService_DeviceLimitExceededDoesNotCreateNewTrial(t *testing.T) {
	policy := NewConfigurableAccessSessionPolicy(AccessSessionPolicyConfig{MaxDevices: 1})
	svc, _, devices, trials, grants, _ := newAccessSessionTestService(policy)
	if _, err := svc.RegisterOrRestore(context.Background(), AccessSessionInput{Email: "writer@example.com", DeviceID: "device-1"}); err != nil {
		t.Fatalf("first device: %v", err)
	}
	_, err := svc.RegisterOrRestore(context.Background(), AccessSessionInput{Email: "writer@example.com", DeviceID: "device-2"})
	if !errors.Is(err, ErrDeviceLimitExceeded) {
		t.Fatalf("expected device limit, got %v", err)
	}
	if len(devices.devices) != 1 || len(trials.grants) != 1 || len(grants.grants) != len(CurrentAdvancedEntitlements()) {
		t.Fatalf("device-limit rejection should not allocate trial/device, devices=%d trials=%d grants=%d", len(devices.devices), len(trials.grants), len(grants.grants))
	}
}

func TestAccessSessionPolicy_NormalizesTrialConfig(t *testing.T) {
	policy := NewConfigurableAccessSessionPolicy(AccessSessionPolicyConfig{
		TrialDurationDays: 7,
		MaxDevices:        3,
		TrialEntitlements: []string{domain.EntitlementEditorialStudio, "", domain.EntitlementEditorialStudio},
	})
	plan := policy.TrialPlan(context.Background(), AccessSessionInput{})
	if plan.GrantType != domain.TrialGrantTypeProOwnAI || plan.DurationDays != 7 || len(plan.EntitlementIDs) != 1 {
		t.Fatalf("unexpected normalized plan: %#v", plan)
	}
	if policy.MaxDevices(context.Background(), AccessSessionInput{}) != 3 {
		t.Fatalf("expected configured max devices")
	}
}

func TestAccessSessionService_RevokedDeviceCannotRestore(t *testing.T) {
	svc, _, devices, _, _, _ := newAccessSessionTestService(nil)
	if _, err := svc.RegisterOrRestore(context.Background(), AccessSessionInput{Email: "writer@example.com", DeviceID: "device-1"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	device := devices.devices["usr_1:device-1"]
	if device == nil {
		// User IDs are generated, so find the only device if the deterministic key is unknown.
		for _, candidate := range devices.devices {
			device = candidate
		}
	}
	if device == nil {
		t.Fatalf("expected created device")
	}
	device.Status = domain.DeviceStatusDisabled
	_, err := svc.RegisterOrRestore(context.Background(), AccessSessionInput{Email: "writer@example.com", DeviceID: device.DeviceID})
	if !errors.Is(err, ErrAccessDeviceRevoked) {
		t.Fatalf("expected revoked device error, got %v", err)
	}
}
