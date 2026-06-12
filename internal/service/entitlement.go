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
	ErrUserNotFound         = errors.New("user not found")
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrInvalidRegistration  = errors.New("invalid registration")
	ErrInvalidGrant         = errors.New("invalid entitlement grant")
	ErrUnknownEntitlement   = errors.New("unknown entitlement")
)

// EntitlementCatalog validates stable entitlement IDs independently from products.
type EntitlementCatalog interface {
	HasEntitlement(id string) bool
}

type StaticEntitlementCatalog struct {
	ids map[string]struct{}
}

func NewStaticEntitlementCatalog(ids ...string) *StaticEntitlementCatalog {
	catalog := &StaticEntitlementCatalog{ids: make(map[string]struct{})}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			catalog.ids[id] = struct{}{}
		}
	}
	return catalog
}

func DefaultEntitlementCatalog() EntitlementCatalog {
	return NewStaticEntitlementCatalog(domain.EntitlementEditorialStudio)
}

func (c *StaticEntitlementCatalog) HasEntitlement(id string) bool {
	_, ok := c.ids[id]
	return ok
}

type RegistrationInput struct {
	Email                string
	DisplayName          string
	RequestedEntitlement string
	DeviceID             string
	Source               string
	Note                 string
}

type RegistrationResult struct {
	User         *domain.User                `json:"user"`
	Registration *domain.RegistrationRequest `json:"registration"`
}

type ReviewRegistrationInput struct {
	RegistrationID string
	Status         string
	ReviewedBy     string
	ReviewNote     string
}

type GrantInput struct {
	UserID         string
	RegistrationID string
	EntitlementID  string
	CreatedBy      string
	Source         string
	ExpiresAt      *time.Time
	IdempotencyKey string
}

// EntitlementService is the billing-side facade for registration, manual grants,
// and access snapshots consumed by app feature gates.
type EntitlementService interface {
	SubmitRegistration(ctx context.Context, input RegistrationInput) (*RegistrationResult, error)
	ListRegistrations(ctx context.Context, query repository.RegistrationQuery) ([]domain.RegistrationRequest, error)
	ReviewRegistration(ctx context.Context, input ReviewRegistrationInput) (*domain.RegistrationRequest, error)
	CreateGrant(ctx context.Context, input GrantInput) (*domain.EntitlementGrant, error)
	ListGrants(ctx context.Context, query repository.EntitlementGrantQuery) ([]domain.EntitlementGrant, error)
	SnapshotForUser(ctx context.Context, userID string) (*domain.EntitlementSnapshot, error)
}

type entitlementServiceImpl struct {
	users          repository.UserRepository
	registrations  repository.RegistrationRepository
	grants         repository.EntitlementGrantRepository
	creditAccounts repository.CreditAccountRepository
	catalog        EntitlementCatalog
}

func NewEntitlementService(
	users repository.UserRepository,
	registrations repository.RegistrationRepository,
	grants repository.EntitlementGrantRepository,
	catalog EntitlementCatalog,
) EntitlementService {
	return NewEntitlementServiceWithCredits(users, registrations, grants, nil, catalog)
}

func NewEntitlementServiceWithCredits(
	users repository.UserRepository,
	registrations repository.RegistrationRepository,
	grants repository.EntitlementGrantRepository,
	creditAccounts repository.CreditAccountRepository,
	catalog EntitlementCatalog,
) EntitlementService {
	if catalog == nil {
		catalog = DefaultEntitlementCatalog()
	}
	return &entitlementServiceImpl{
		users:          users,
		registrations:  registrations,
		grants:         grants,
		creditAccounts: creditAccounts,
		catalog:        catalog,
	}
}

func (s *entitlementServiceImpl) SubmitRegistration(ctx context.Context, input RegistrationInput) (*RegistrationResult, error) {
	email := normalizeEmail(input.Email)
	if email == "" {
		return nil, ErrInvalidRegistration
	}

	requestedEntitlement := strings.TrimSpace(input.RequestedEntitlement)
	if requestedEntitlement == "" {
		requestedEntitlement = domain.EntitlementEditorialStudio
	}
	if !s.catalog.HasEntitlement(requestedEntitlement) {
		return nil, ErrUnknownEntitlement
	}

	now := time.Now().UTC()
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		userID, err := generateEntityID("usr_")
		if err != nil {
			return nil, err
		}
		user = &domain.User{
			ID:          userID,
			Email:       email,
			DisplayName: strings.TrimSpace(input.DisplayName),
			Status:      domain.UserStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.users.Create(ctx, user); err != nil {
			return nil, err
		}
	} else if user.DisplayName == "" && strings.TrimSpace(input.DisplayName) != "" {
		user.DisplayName = strings.TrimSpace(input.DisplayName)
		user.UpdatedAt = now
		if err := s.users.Update(ctx, user); err != nil {
			return nil, err
		}
	}

	registrationID, err := generateEntityID("reg_")
	if err != nil {
		return nil, err
	}
	registration := &domain.RegistrationRequest{
		ID:                   registrationID,
		UserID:               user.ID,
		Email:                email,
		DisplayName:          strings.TrimSpace(input.DisplayName),
		RequestedEntitlement: requestedEntitlement,
		Status:               domain.RegistrationStatusPending,
		Source:               defaultString(strings.TrimSpace(input.Source), "desktop"),
		DeviceID:             strings.TrimSpace(input.DeviceID),
		Note:                 strings.TrimSpace(input.Note),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.registrations.Create(ctx, registration); err != nil {
		return nil, err
	}

	return &RegistrationResult{User: user, Registration: registration}, nil
}

func (s *entitlementServiceImpl) ListRegistrations(ctx context.Context, query repository.RegistrationQuery) ([]domain.RegistrationRequest, error) {
	query.Email = normalizeEmail(query.Email)
	return s.registrations.List(ctx, query)
}

func (s *entitlementServiceImpl) ReviewRegistration(ctx context.Context, input ReviewRegistrationInput) (*domain.RegistrationRequest, error) {
	registrationID := strings.TrimSpace(input.RegistrationID)
	if registrationID == "" || !validRegistrationStatus(input.Status) {
		return nil, ErrInvalidRegistration
	}

	registration, err := s.registrations.GetByID(ctx, registrationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrRegistrationNotFound
		}
		return nil, err
	}

	now := time.Now().UTC()
	registration.Status = input.Status
	registration.ReviewedBy = defaultString(strings.TrimSpace(input.ReviewedBy), "admin")
	registration.ReviewNote = strings.TrimSpace(input.ReviewNote)
	registration.ReviewedAt = &now
	registration.UpdatedAt = now
	if err := s.registrations.Update(ctx, registration); err != nil {
		return nil, err
	}
	return registration, nil
}

func (s *entitlementServiceImpl) CreateGrant(ctx context.Context, input GrantInput) (*domain.EntitlementGrant, error) {
	userID := strings.TrimSpace(input.UserID)
	entitlementID := strings.TrimSpace(input.EntitlementID)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)

	if strings.TrimSpace(input.RegistrationID) != "" {
		registration, err := s.registrations.GetByID(ctx, strings.TrimSpace(input.RegistrationID))
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, ErrRegistrationNotFound
			}
			return nil, err
		}
		if userID != "" && userID != registration.UserID {
			return nil, ErrInvalidGrant
		}
		if entitlementID != "" && entitlementID != registration.RequestedEntitlement {
			return nil, ErrInvalidGrant
		}
		userID = registration.UserID
		entitlementID = registration.RequestedEntitlement
	}

	return createGrantWithRepos(ctx, s.users, s.grants, s.catalog, GrantInput{
		UserID:         userID,
		EntitlementID:  entitlementID,
		CreatedBy:      input.CreatedBy,
		Source:         input.Source,
		ExpiresAt:      input.ExpiresAt,
		IdempotencyKey: idempotencyKey,
	})
}

func createGrantWithRepos(
	ctx context.Context,
	users repository.UserRepository,
	grants repository.EntitlementGrantRepository,
	catalog EntitlementCatalog,
	input GrantInput,
) (*domain.EntitlementGrant, error) {
	userID := strings.TrimSpace(input.UserID)
	entitlementID := strings.TrimSpace(input.EntitlementID)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if userID == "" || entitlementID == "" || grants == nil {
		return nil, ErrInvalidGrant
	}
	if catalog == nil {
		catalog = DefaultEntitlementCatalog()
	}
	if idempotencyKey != "" {
		existing, err := grants.GetByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
			if existing.UserID != userID || existing.EntitlementID != entitlementID {
				return nil, ErrInvalidGrant
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if !catalog.HasEntitlement(entitlementID) {
		return nil, ErrUnknownEntitlement
	}
	if users != nil {
		if _, err := users.GetByID(ctx, userID); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, ErrUserNotFound
			}
			return nil, err
		}
	}

	now := time.Now().UTC()
	if idempotencyKey == "" {
		existing, err := grants.ListByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		for idx := range existing {
			grant := existing[idx]
			if grant.EntitlementID == entitlementID && isGrantActive(grant, now) {
				return &grant, nil
			}
		}
	}

	grantID, err := generateEntityID("grt_")
	if err != nil {
		return nil, err
	}
	var idempotencyKeyPtr *string
	if idempotencyKey != "" {
		idempotencyKeyPtr = &idempotencyKey
	}
	grant := &domain.EntitlementGrant{
		ID:             grantID,
		UserID:         userID,
		EntitlementID:  entitlementID,
		Status:         domain.GrantStatusActive,
		Source:         defaultString(strings.TrimSpace(input.Source), domain.GrantSourceManual),
		StartsAt:       now,
		ExpiresAt:      input.ExpiresAt,
		CreatedBy:      defaultString(strings.TrimSpace(input.CreatedBy), "admin"),
		IdempotencyKey: idempotencyKeyPtr,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := grants.Create(ctx, grant); err != nil {
		return nil, err
	}
	return grant, nil
}

func (s *entitlementServiceImpl) ListGrants(ctx context.Context, query repository.EntitlementGrantQuery) ([]domain.EntitlementGrant, error) {
	return s.grants.List(ctx, query)
}

func (s *entitlementServiceImpl) SnapshotForUser(ctx context.Context, userID string) (*domain.EntitlementSnapshot, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	grants, err := s.grants.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	entitlements := make(map[string]bool)
	for _, grant := range grants {
		if isGrantActive(grant, now) {
			entitlements[grant.EntitlementID] = true
		}
	}

	credits := s.creditSnapshot(ctx, userID)

	return &domain.EntitlementSnapshot{
		User: domain.EntitlementSnapshotUser{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Status:      user.Status,
		},
		Entitlements: entitlements,
		Features:     map[string]any{},
		Credits:      credits,
		UpdatedAt:    now.Format(time.RFC3339),
		Source:       "billing_provider",
	}, nil
}

func (s *entitlementServiceImpl) creditSnapshot(ctx context.Context, userID string) map[string]int64 {
	credits := map[string]int64{}
	if s.creditAccounts == nil {
		return credits
	}
	account, err := s.creditAccounts.GetByUserID(ctx, userID)
	if err != nil {
		return credits
	}
	credits[domain.CreditMetricBalance] = account.Balance
	credits[domain.CreditMetricReserved] = account.Reserved
	return credits
}

func validRegistrationStatus(status string) bool {
	switch status {
	case domain.RegistrationStatusPending, domain.RegistrationStatusApproved, domain.RegistrationStatusRejected:
		return true
	default:
		return false
	}
}

func isGrantActive(grant domain.EntitlementGrant, now time.Time) bool {
	if grant.Status != domain.GrantStatusActive {
		return false
	}
	if !grant.StartsAt.IsZero() && grant.StartsAt.After(now) {
		return false
	}
	if grant.ExpiresAt != nil && !grant.ExpiresAt.After(now) {
		return false
	}
	return true
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
