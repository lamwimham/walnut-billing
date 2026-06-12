package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockEntitlementUserRepo struct {
	users map[string]*domain.User
}

func newMockEntitlementUserRepo() *mockEntitlementUserRepo {
	return &mockEntitlementUserRepo{users: make(map[string]*domain.User)}
}

func (m *mockEntitlementUserRepo) Create(ctx context.Context, user *domain.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockEntitlementUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	user, ok := m.users[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return user, nil
}

func (m *mockEntitlementUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	for _, user := range m.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockEntitlementUserRepo) Update(ctx context.Context, user *domain.User) error {
	m.users[user.ID] = user
	return nil
}

type mockRegistrationRepo struct {
	registrations map[string]*domain.RegistrationRequest
}

func newMockRegistrationRepo() *mockRegistrationRepo {
	return &mockRegistrationRepo{registrations: make(map[string]*domain.RegistrationRequest)}
}

func (m *mockRegistrationRepo) Create(ctx context.Context, registration *domain.RegistrationRequest) error {
	m.registrations[registration.ID] = registration
	return nil
}

func (m *mockRegistrationRepo) GetByID(ctx context.Context, id string) (*domain.RegistrationRequest, error) {
	registration, ok := m.registrations[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return registration, nil
}

func (m *mockRegistrationRepo) List(ctx context.Context, query repository.RegistrationQuery) ([]domain.RegistrationRequest, error) {
	var result []domain.RegistrationRequest
	for _, registration := range m.registrations {
		if query.Status != "" && registration.Status != query.Status {
			continue
		}
		if query.UserID != "" && registration.UserID != query.UserID {
			continue
		}
		if query.Email != "" && registration.Email != query.Email {
			continue
		}
		result = append(result, *registration)
	}
	return result, nil
}

func (m *mockRegistrationRepo) Update(ctx context.Context, registration *domain.RegistrationRequest) error {
	m.registrations[registration.ID] = registration
	return nil
}

type mockGrantRepo struct {
	grants map[string]*domain.EntitlementGrant
}

func newMockGrantRepo() *mockGrantRepo {
	return &mockGrantRepo{grants: make(map[string]*domain.EntitlementGrant)}
}

func (m *mockGrantRepo) Create(ctx context.Context, grant *domain.EntitlementGrant) error {
	m.grants[grant.ID] = grant
	return nil
}

func (m *mockGrantRepo) GetByID(ctx context.Context, id string) (*domain.EntitlementGrant, error) {
	grant, ok := m.grants[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return grant, nil
}

func (m *mockGrantRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.EntitlementGrant, error) {
	for _, grant := range m.grants {
		if grant.IdempotencyKey != nil && *grant.IdempotencyKey == key {
			return grant, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockGrantRepo) List(ctx context.Context, query repository.EntitlementGrantQuery) ([]domain.EntitlementGrant, error) {
	var result []domain.EntitlementGrant
	for _, grant := range m.grants {
		if query.UserID != "" && grant.UserID != query.UserID {
			continue
		}
		if query.EntitlementID != "" && grant.EntitlementID != query.EntitlementID {
			continue
		}
		if query.Status != "" && grant.Status != query.Status {
			continue
		}
		result = append(result, *grant)
	}
	return result, nil
}

func (m *mockGrantRepo) ListByUser(ctx context.Context, userID string) ([]domain.EntitlementGrant, error) {
	return m.List(ctx, repository.EntitlementGrantQuery{UserID: userID, IncludeExpired: true})
}

func (m *mockGrantRepo) Update(ctx context.Context, grant *domain.EntitlementGrant) error {
	m.grants[grant.ID] = grant
	return nil
}

func newEntitlementTestService() (EntitlementService, *mockEntitlementUserRepo, *mockRegistrationRepo, *mockGrantRepo) {
	users := newMockEntitlementUserRepo()
	registrations := newMockRegistrationRepo()
	grants := newMockGrantRepo()
	return NewEntitlementService(users, registrations, grants, DefaultEntitlementCatalog()), users, registrations, grants
}

func TestEntitlementService_SubmitRegistrationCreatesUserAndPendingRequest(t *testing.T) {
	svc, users, registrations, _ := newEntitlementTestService()

	result, err := svc.SubmitRegistration(context.Background(), RegistrationInput{
		Email:       "Writer@Example.COM ",
		DisplayName: "Writer",
		DeviceID:    "device-1",
		Source:      "desktop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.User.Email != "writer@example.com" {
		t.Fatalf("expected normalized email, got %s", result.User.Email)
	}
	if result.Registration.Status != domain.RegistrationStatusPending {
		t.Fatalf("expected pending registration, got %s", result.Registration.Status)
	}
	if result.Registration.RequestedEntitlement != domain.EntitlementEditorialStudio {
		t.Fatalf("expected default editorial entitlement, got %s", result.Registration.RequestedEntitlement)
	}
	if len(users.users) != 1 || len(registrations.registrations) != 1 {
		t.Fatalf("expected one user and one registration")
	}
}

func TestEntitlementService_CreateGrantIsIdempotentForActiveEntitlement(t *testing.T) {
	svc, users, _, grants := newEntitlementTestService()
	user := &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	users.users[user.ID] = user

	first, err := svc.CreateGrant(context.Background(), GrantInput{
		UserID:        user.ID,
		EntitlementID: domain.EntitlementEditorialStudio,
		CreatedBy:     "admin",
	})
	if err != nil {
		t.Fatalf("expected first grant, got %v", err)
	}
	second, err := svc.CreateGrant(context.Background(), GrantInput{
		UserID:        user.ID,
		EntitlementID: domain.EntitlementEditorialStudio,
		CreatedBy:     "admin",
	})
	if err != nil {
		t.Fatalf("expected idempotent grant, got %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected existing grant to be returned")
	}
	if len(grants.grants) != 1 {
		t.Fatalf("expected one grant, got %d", len(grants.grants))
	}
}

func TestEntitlementService_CreateGrantUsesIdempotencyKeyForFulfillment(t *testing.T) {
	svc, users, _, grants := newEntitlementTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}

	first, err := svc.CreateGrant(context.Background(), GrantInput{
		UserID:         "usr_1",
		EntitlementID:  domain.EntitlementEditorialStudio,
		Source:         domain.GrantSourceFulfillment,
		IdempotencyKey: "fulfillment:CHK-1:entitlement",
	})
	if err != nil {
		t.Fatalf("expected first fulfillment grant, got %v", err)
	}
	second, err := svc.CreateGrant(context.Background(), GrantInput{
		UserID:         "usr_1",
		EntitlementID:  domain.EntitlementEditorialStudio,
		Source:         domain.GrantSourceFulfillment,
		IdempotencyKey: "fulfillment:CHK-1:entitlement",
	})
	if err != nil {
		t.Fatalf("expected idempotent fulfillment grant, got %v", err)
	}
	if first.ID != second.ID || len(grants.grants) != 1 {
		t.Fatalf("expected one idempotent grant, first=%s second=%s total=%d", first.ID, second.ID, len(grants.grants))
	}
}

func TestEntitlementService_SnapshotIncludesOnlyActiveGrants(t *testing.T) {
	svc, users, _, grants := newEntitlementTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	grants.grants["active"] = &domain.EntitlementGrant{
		ID:            "active",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		StartsAt:      now.Add(-time.Hour),
	}
	grants.grants["expired"] = &domain.EntitlementGrant{
		ID:            "expired",
		UserID:        "usr_1",
		EntitlementID: "legacy.feature",
		Status:        domain.GrantStatusActive,
		StartsAt:      now.Add(-2 * time.Hour),
		ExpiresAt:     &expired,
	}
	grants.grants["revoked"] = &domain.EntitlementGrant{
		ID:            "revoked",
		UserID:        "usr_1",
		EntitlementID: "revoked.feature",
		Status:        domain.GrantStatusRevoked,
		StartsAt:      now.Add(-time.Hour),
	}

	snapshot, err := svc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("expected snapshot, got %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] {
		t.Fatalf("expected editorial studio entitlement")
	}
	if snapshot.Entitlements["legacy.feature"] || snapshot.Entitlements["revoked.feature"] {
		t.Fatalf("expected expired/revoked grants to be excluded: %#v", snapshot.Entitlements)
	}
	if snapshot.Source != "billing_provider" {
		t.Fatalf("expected billing_provider source, got %s", snapshot.Source)
	}
}

func TestEntitlementService_RejectsUnknownEntitlement(t *testing.T) {
	svc, users, _, _ := newEntitlementTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}

	_, err := svc.CreateGrant(context.Background(), GrantInput{UserID: "usr_1", EntitlementID: "unknown.feature"})
	if !errors.Is(err, ErrUnknownEntitlement) {
		t.Fatalf("expected unknown entitlement, got %v", err)
	}
}
