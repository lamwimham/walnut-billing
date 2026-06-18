package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidAccessSession = errors.New("invalid access session")
	ErrDeviceLimitExceeded  = errors.New("device limit exceeded")
	ErrAccessUserDisabled   = errors.New("access user disabled")
)

type AccessSessionService interface {
	RegisterOrRestore(ctx context.Context, input AccessSessionInput) (*AccessSessionResult, error)
}

type AccessSessionInput struct {
	Email       string
	DisplayName string
	DeviceID    string
	Source      string
	Note        string
}

type AccessSessionResult struct {
	User           *domain.User                `json:"user"`
	Device         *domain.UserDevice          `json:"device"`
	Trial          *domain.TrialGrant          `json:"trial,omitempty"`
	TrialCreated   bool                        `json:"trial_created"`
	Snapshot       *domain.EntitlementSnapshot `json:"snapshot"`
	AccessSnapshot *domain.AccessSnapshotV2    `json:"access_snapshot,omitempty"`
	Source         string                      `json:"source"`
}

type AccessSessionRepositories struct {
	Users             repository.UserRepository
	Devices           repository.UserDeviceRepository
	TrialGrants       repository.TrialGrantRepository
	EntitlementGrants repository.EntitlementGrantRepository
	CreditAccounts    repository.CreditAccountRepository
}

type AccessSessionDependencies struct {
	Repositories       AccessSessionRepositories
	Policy             AccessSessionPolicy
	EntitlementCatalog EntitlementCatalog
	SnapshotIssuer     AccessSnapshotIssuer
	UnitOfWorkFactory  func() repository.UnitOfWork
}

type AccessSessionPolicy interface {
	TrialPlan(ctx context.Context, input AccessSessionInput) TrialAccessPlan
	MaxDevices(ctx context.Context, input AccessSessionInput) int
}

type TrialAccessPlan struct {
	GrantType      string
	DurationDays   int
	EntitlementIDs []string
}

type AccessSessionPolicyConfig struct {
	TrialGrantType    string
	TrialDurationDays int
	MaxDevices        int
	TrialEntitlements []string
}

type configurableAccessSessionPolicy struct {
	config AccessSessionPolicyConfig
}

func DefaultAccessSessionPolicyConfig() AccessSessionPolicyConfig {
	return AccessSessionPolicyConfig{
		TrialGrantType:    domain.TrialGrantTypeProOwnAI,
		TrialDurationDays: 14,
		MaxDevices:        2,
		TrialEntitlements: CurrentAdvancedEntitlements(),
	}
}

func NewConfigurableAccessSessionPolicy(config AccessSessionPolicyConfig) AccessSessionPolicy {
	return &configurableAccessSessionPolicy{config: normalizeAccessSessionPolicyConfig(config)}
}

func DefaultAccessSessionPolicy() AccessSessionPolicy {
	return NewConfigurableAccessSessionPolicy(DefaultAccessSessionPolicyConfig())
}

func (p *configurableAccessSessionPolicy) TrialPlan(ctx context.Context, input AccessSessionInput) TrialAccessPlan {
	_ = ctx
	_ = input
	entitlements := make([]string, len(p.config.TrialEntitlements))
	copy(entitlements, p.config.TrialEntitlements)
	return TrialAccessPlan{
		GrantType:      p.config.TrialGrantType,
		DurationDays:   p.config.TrialDurationDays,
		EntitlementIDs: entitlements,
	}
}

func (p *configurableAccessSessionPolicy) MaxDevices(ctx context.Context, input AccessSessionInput) int {
	_ = ctx
	_ = input
	return p.config.MaxDevices
}

type accessSessionServiceImpl struct {
	repos          AccessSessionRepositories
	policy         AccessSessionPolicy
	catalog        EntitlementCatalog
	snapshotIssuer AccessSnapshotIssuer
	uowFactory     func() repository.UnitOfWork
}

func NewAccessSessionService(deps AccessSessionDependencies) AccessSessionService {
	policy := deps.Policy
	if policy == nil {
		policy = DefaultAccessSessionPolicy()
	}
	catalog := deps.EntitlementCatalog
	if catalog == nil {
		catalog = DefaultEntitlementCatalog()
	}
	return &accessSessionServiceImpl{
		repos:          deps.Repositories,
		policy:         policy,
		catalog:        catalog,
		snapshotIssuer: deps.SnapshotIssuer,
		uowFactory:     deps.UnitOfWorkFactory,
	}
}

func (s *accessSessionServiceImpl) RegisterOrRestore(ctx context.Context, input AccessSessionInput) (*AccessSessionResult, error) {
	if s == nil || !s.hasRequiredRepos(s.repos) {
		return nil, ErrInvalidAccessSession
	}
	input.Email = normalizeEmail(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.Source = defaultString(strings.TrimSpace(input.Source), "desktop")
	input.Note = strings.TrimSpace(input.Note)
	if input.Email == "" || input.DeviceID == "" {
		return nil, ErrInvalidAccessSession
	}
	result, err := s.withAccessSessionTransaction(ctx, func(repos AccessSessionRepositories) (*AccessSessionResult, error) {
		return s.registerOrRestoreWithRepos(ctx, repos, input)
	})
	if err != nil || result == nil || s.snapshotIssuer == nil {
		return result, err
	}
	accessSnapshot, snapshotErr := s.snapshotIssuer.Issue(ctx, AccessSnapshotIssueInput{UserID: result.User.ID, DeviceID: result.Device.DeviceID})
	if snapshotErr != nil {
		return result, snapshotErr
	}
	result.AccessSnapshot = accessSnapshot
	return result, nil
}

func (s *accessSessionServiceImpl) registerOrRestoreWithRepos(ctx context.Context, repos AccessSessionRepositories, input AccessSessionInput) (*AccessSessionResult, error) {
	now := time.Now().UTC()
	user, err := s.upsertUser(ctx, repos.Users, input, now)
	if err != nil {
		return nil, err
	}
	device, err := s.bindDevice(ctx, repos.Devices, user.ID, input.DeviceID, s.policy.MaxDevices(ctx, input), now)
	if err != nil {
		return nil, err
	}
	trial, trialCreated, err := s.ensureTrial(ctx, repos, user, input, s.policy.TrialPlan(ctx, input), now)
	if err != nil {
		return nil, err
	}
	snapshot, err := snapshotForUserWithRepos(ctx, repos.Users, repos.EntitlementGrants, repos.CreditAccounts, user.ID)
	if err != nil {
		return nil, err
	}
	return &AccessSessionResult{
		User:         user,
		Device:       device,
		Trial:        trial,
		TrialCreated: trialCreated,
		Snapshot:     snapshot,
		Source:       "billing_provider",
	}, nil
}

func (s *accessSessionServiceImpl) upsertUser(ctx context.Context, users repository.UserRepository, input AccessSessionInput, now time.Time) (*domain.User, error) {
	user, err := users.GetByEmail(ctx, input.Email)
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
			Email:       input.Email,
			DisplayName: input.DisplayName,
			Status:      domain.UserStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := users.Create(ctx, user); err != nil {
			return nil, err
		}
		return user, nil
	}
	if user.Status != "" && user.Status != domain.UserStatusActive {
		return nil, ErrAccessUserDisabled
	}
	if user.DisplayName == "" && input.DisplayName != "" {
		user.DisplayName = input.DisplayName
		user.UpdatedAt = now
		if err := users.Update(ctx, user); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func (s *accessSessionServiceImpl) bindDevice(ctx context.Context, devices repository.UserDeviceRepository, userID string, deviceID string, maxDevices int, now time.Time) (*domain.UserDevice, error) {
	if maxDevices <= 0 {
		maxDevices = DefaultAccessSessionPolicyConfig().MaxDevices
	}
	device, err := devices.GetByUserAndDevice(ctx, userID, deviceID)
	if err == nil {
		if device.Status != "" && device.Status != domain.DeviceStatusActive {
			return nil, ErrDeviceLimitExceeded
		}
		device.Status = domain.DeviceStatusActive
		device.LastSeenAt = now
		device.UpdatedAt = now
		if err := devices.Update(ctx, device); err != nil {
			return nil, err
		}
		return device, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	activeDevices, err := devices.ListByUser(ctx, userID, domain.DeviceStatusActive)
	if err != nil {
		return nil, err
	}
	if len(activeDevices) >= maxDevices {
		return nil, ErrDeviceLimitExceeded
	}
	id, err := generateEntityID("dev_")
	if err != nil {
		return nil, err
	}
	device = &domain.UserDevice{
		ID:          id,
		UserID:      userID,
		DeviceID:    deviceID,
		Status:      domain.DeviceStatusActive,
		FirstSeenAt: now,
		LastSeenAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := devices.Create(ctx, device); err != nil {
		return nil, err
	}
	return device, nil
}

func (s *accessSessionServiceImpl) ensureTrial(ctx context.Context, repos AccessSessionRepositories, user *domain.User, input AccessSessionInput, plan TrialAccessPlan, now time.Time) (*domain.TrialGrant, bool, error) {
	plan = normalizeTrialAccessPlan(plan)
	if user == nil || plan.GrantType == "" || plan.DurationDays <= 0 || len(plan.EntitlementIDs) == 0 {
		return nil, false, ErrInvalidAccessSession
	}
	key := trialGrantIdempotencyKey(input.Email, plan.GrantType)
	trial, err := repos.TrialGrants.GetByIdempotencyKey(ctx, key)
	if err == nil {
		if trial.UserID != user.ID || trial.Email != input.Email || trial.GrantType != plan.GrantType {
			return nil, false, ErrInvalidAccessSession
		}
		if err := s.ensureTrialEntitlementGrants(ctx, repos, user.ID, trial, plan.EntitlementIDs); err != nil {
			return nil, false, err
		}
		return trial, false, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, false, err
	}
	expiresAt := now.AddDate(0, 0, plan.DurationDays)
	trialID, err := generateEntityID("trl_")
	if err != nil {
		return nil, false, err
	}
	trial = &domain.TrialGrant{
		ID:             trialID,
		UserID:         user.ID,
		Email:          input.Email,
		GrantType:      plan.GrantType,
		Status:         domain.TrialGrantStatusIssued,
		StartsAt:       now,
		ExpiresAt:      &expiresAt,
		IdempotencyKey: key,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := repos.TrialGrants.Create(ctx, trial); err != nil {
		return nil, false, err
	}
	if err := s.ensureTrialEntitlementGrants(ctx, repos, user.ID, trial, plan.EntitlementIDs); err != nil {
		return nil, false, err
	}
	return trial, true, nil
}

func (s *accessSessionServiceImpl) ensureTrialEntitlementGrants(ctx context.Context, repos AccessSessionRepositories, userID string, trial *domain.TrialGrant, entitlementIDs []string) error {
	if trial == nil {
		return ErrInvalidAccessSession
	}
	for _, entitlementID := range entitlementIDs {
		entitlementID = strings.TrimSpace(entitlementID)
		if entitlementID == "" {
			continue
		}
		if _, err := createGrantWithRepos(ctx, repos.Users, repos.EntitlementGrants, s.catalog, GrantInput{
			UserID:         userID,
			EntitlementID:  entitlementID,
			CreatedBy:      "system",
			Source:         domain.GrantSourceTrial,
			StartsAt:       trial.StartsAt,
			ExpiresAt:      trial.ExpiresAt,
			IdempotencyKey: trialEntitlementGrantKey(trial.IdempotencyKey, entitlementID),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *accessSessionServiceImpl) withAccessSessionTransaction(ctx context.Context, fn func(AccessSessionRepositories) (*AccessSessionResult, error)) (*AccessSessionResult, error) {
	if s.uowFactory == nil {
		return fn(s.repos)
	}
	uow := s.uowFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback()
		}
	}()

	result, err := fn(accessSessionReposFromUOW(uow.Repos(), s.repos))
	if err != nil {
		return result, err
	}
	if err := uow.Commit(); err != nil {
		return result, err
	}
	committed = true
	return result, nil
}

func (s *accessSessionServiceImpl) hasRequiredRepos(repos AccessSessionRepositories) bool {
	return repos.Users != nil && repos.Devices != nil && repos.TrialGrants != nil && repos.EntitlementGrants != nil
}

func accessSessionReposFromUOW(repos repository.TransactionalRepositories, fallback AccessSessionRepositories) AccessSessionRepositories {
	return AccessSessionRepositories{
		Users:             firstUserRepo(repos.UserRepo, fallback.Users),
		Devices:           firstUserDeviceRepo(repos.UserDeviceRepo, fallback.Devices),
		TrialGrants:       firstTrialGrantRepo(repos.TrialGrantRepo, fallback.TrialGrants),
		EntitlementGrants: firstEntitlementGrantRepo(repos.EntitlementGrantRepo, fallback.EntitlementGrants),
		CreditAccounts:    firstCreditAccountRepo(repos.CreditAccountRepo, fallback.CreditAccounts),
	}
}

func firstUserDeviceRepo(primary repository.UserDeviceRepository, fallback repository.UserDeviceRepository) repository.UserDeviceRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstTrialGrantRepo(primary repository.TrialGrantRepository, fallback repository.TrialGrantRepository) repository.TrialGrantRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func normalizeAccessSessionPolicyConfig(config AccessSessionPolicyConfig) AccessSessionPolicyConfig {
	defaults := DefaultAccessSessionPolicyConfig()
	config.TrialGrantType = strings.TrimSpace(config.TrialGrantType)
	if config.TrialGrantType == "" {
		config.TrialGrantType = defaults.TrialGrantType
	}
	if config.TrialDurationDays <= 0 {
		config.TrialDurationDays = defaults.TrialDurationDays
	}
	if config.MaxDevices <= 0 {
		config.MaxDevices = defaults.MaxDevices
	}
	if len(config.TrialEntitlements) == 0 {
		config.TrialEntitlements = defaults.TrialEntitlements
	}
	config.TrialEntitlements = normalizeStringSet(config.TrialEntitlements)
	return config
}

func normalizeTrialAccessPlan(plan TrialAccessPlan) TrialAccessPlan {
	plan.GrantType = strings.TrimSpace(plan.GrantType)
	plan.EntitlementIDs = normalizeStringSet(plan.EntitlementIDs)
	return plan
}

func normalizeStringSet(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func trialGrantIdempotencyKey(email string, grantType string) string {
	return stableAccessSessionKey("trial", strings.TrimSpace(grantType), normalizeEmail(email))
}

func trialEntitlementGrantKey(trialKey string, entitlementID string) string {
	return stableAccessSessionKey("trial_entitlement", strings.TrimSpace(trialKey), strings.TrimSpace(entitlementID))
}

func stableAccessSessionKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strings.TrimSpace(part)))
		h.Write([]byte{0})
	}
	return "acs_" + hex.EncodeToString(h.Sum(nil))
}
