package service

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidAccessSnapshot = errors.New("invalid access snapshot")
	ErrSnapshotSignature     = errors.New("access snapshot signature invalid")
)

const (
	AccessLicenseStateBasic        = "basic"
	AccessLicenseStateTrial        = "trial"
	AccessLicenseStateSubscription = "subscription"
	AccessLicenseStateLifetime     = "lifetime"
	AccessLicenseStateGrace        = "grace"
	AccessPlanProOwnAITrial        = "pro_own_ai_trial"
	AccessAIModeBYOK               = "byok"
)

type AccessSnapshotIssuer interface {
	Issue(ctx context.Context, input AccessSnapshotIssueInput) (*domain.AccessSnapshotV2, error)
}

type AccessSnapshotIssueInput struct {
	UserID   string
	DeviceID string
}

type AccessSnapshotIssuerRepositories struct {
	Users             repository.UserRepository
	Devices           repository.UserDeviceRepository
	TrialGrants       repository.TrialGrantRepository
	EntitlementGrants repository.EntitlementGrantRepository
	CreditAccounts    repository.CreditAccountRepository
	Orders            repository.OrderRepository
	Cancellations     repository.SubscriptionCancellationRepository
}

type AccessSnapshotIssuerDependencies struct {
	Repositories          AccessSnapshotIssuerRepositories
	Policy                AccessSnapshotPolicy
	CloudQuotaPolicy      CloudStorageQuotaPolicy
	Signer                AccessSnapshotSigner
	SoftwareSubscriptions SoftwareSubscriptionProjector
}

type AccessSnapshotPolicy interface {
	TTL(ctx context.Context, user *domain.User) time.Duration
	OfflineGrace(ctx context.Context, user *domain.User) time.Duration
	MaxDevices(ctx context.Context, user *domain.User) int
	CloudStorageQuotaMB(ctx context.Context, user *domain.User) int64
}

type AccessSnapshotPolicyConfig struct {
	TTLSeconds          int
	OfflineGraceSeconds int
	MaxDevices          int
	CloudStorageQuotaMB int64
}

type configurableAccessSnapshotPolicy struct {
	config AccessSnapshotPolicyConfig
}

func DefaultAccessSnapshotPolicyConfig() AccessSnapshotPolicyConfig {
	return AccessSnapshotPolicyConfig{
		TTLSeconds:          24 * 60 * 60,
		OfflineGraceSeconds: 7 * 24 * 60 * 60,
		MaxDevices:          defaultAccessMaxDevices,
		CloudStorageQuotaMB: 1024,
	}
}

func NewConfigurableAccessSnapshotPolicy(config AccessSnapshotPolicyConfig) AccessSnapshotPolicy {
	return &configurableAccessSnapshotPolicy{config: normalizeAccessSnapshotPolicyConfig(config)}
}

func DefaultAccessSnapshotPolicy() AccessSnapshotPolicy {
	return NewConfigurableAccessSnapshotPolicy(DefaultAccessSnapshotPolicyConfig())
}

func (p *configurableAccessSnapshotPolicy) TTL(ctx context.Context, user *domain.User) time.Duration {
	_ = ctx
	_ = user
	return time.Duration(p.config.TTLSeconds) * time.Second
}

func (p *configurableAccessSnapshotPolicy) OfflineGrace(ctx context.Context, user *domain.User) time.Duration {
	_ = ctx
	_ = user
	return time.Duration(p.config.OfflineGraceSeconds) * time.Second
}

func (p *configurableAccessSnapshotPolicy) MaxDevices(ctx context.Context, user *domain.User) int {
	_ = ctx
	_ = user
	return p.config.MaxDevices
}

func (p *configurableAccessSnapshotPolicy) CloudStorageQuotaMB(ctx context.Context, user *domain.User) int64 {
	_ = ctx
	_ = user
	return p.config.CloudStorageQuotaMB
}

type AccessSnapshotSigner interface {
	Sign(snapshot domain.AccessSnapshotV2) (string, error)
	Verify(snapshot domain.AccessSnapshotV2) error
	KeyID() string
	Algorithm() string
}

type hmacAccessSnapshotSigner struct {
	secret []byte
	keyID  string
}

type ed25519AccessSnapshotSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string
}

func NewHMACAccessSnapshotSigner(secret string, keyID string) (AccessSnapshotSigner, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, ErrInvalidAccessSnapshot
	}
	keyID = defaultString(strings.TrimSpace(keyID), "default")
	return &hmacAccessSnapshotSigner{secret: []byte(secret), keyID: keyID}, nil
}

func DefaultAccessSnapshotSigner() AccessSnapshotSigner {
	signer, err := NewHMACAccessSnapshotSigner("walnut-dev-access-snapshot-secret", "dev")
	if err != nil {
		panic(err)
	}
	return signer
}

func (s *hmacAccessSnapshotSigner) Sign(snapshot domain.AccessSnapshotV2) (string, error) {
	payload, err := canonicalAccessSnapshotPayload(snapshot)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *hmacAccessSnapshotSigner) Verify(snapshot domain.AccessSnapshotV2) error {
	provided := strings.TrimSpace(snapshot.Signature)
	if provided == "" {
		return ErrSnapshotSignature
	}
	expected, err := s.Sign(snapshot)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return ErrSnapshotSignature
	}
	return nil
}

func (s *hmacAccessSnapshotSigner) KeyID() string {
	return s.keyID
}

func (s *hmacAccessSnapshotSigner) Algorithm() string {
	return "HS256"
}

func NewEd25519AccessSnapshotSigner(privateKey string, keyID string) (AccessSnapshotSigner, error) {
	key, err := parseEd25519PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	keyID = defaultString(strings.TrimSpace(keyID), "default")
	publicKey, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, ErrInvalidAccessSnapshot
	}
	return &ed25519AccessSnapshotSigner{privateKey: key, publicKey: publicKey, keyID: keyID}, nil
}

func GenerateEd25519AccessSnapshotKeyPair() (privateKey string, publicKey string, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawStdEncoding.EncodeToString(private), base64.RawStdEncoding.EncodeToString(public), nil
}

func (s *ed25519AccessSnapshotSigner) Sign(snapshot domain.AccessSnapshotV2) (string, error) {
	payload, err := canonicalAccessSnapshotPayload(snapshot)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(s.privateKey, payload)
	return base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *ed25519AccessSnapshotSigner) Verify(snapshot domain.AccessSnapshotV2) error {
	provided := strings.TrimSpace(snapshot.Signature)
	if provided == "" {
		return ErrSnapshotSignature
	}
	signature, err := decodeRawBase64URL(provided)
	if err != nil {
		return ErrSnapshotSignature
	}
	payload, err := canonicalAccessSnapshotPayload(snapshot)
	if err != nil {
		return err
	}
	if !ed25519.Verify(s.publicKey, payload, signature) {
		return ErrSnapshotSignature
	}
	return nil
}

func (s *ed25519AccessSnapshotSigner) KeyID() string {
	return s.keyID
}

func (s *ed25519AccessSnapshotSigner) Algorithm() string {
	return "Ed25519"
}

type accessSnapshotIssuer struct {
	repos                 AccessSnapshotIssuerRepositories
	policy                AccessSnapshotPolicy
	cloudQuotaPolicy      CloudStorageQuotaPolicy
	signer                AccessSnapshotSigner
	softwareSubscriptions SoftwareSubscriptionProjector
}

func NewAccessSnapshotIssuer(deps AccessSnapshotIssuerDependencies) AccessSnapshotIssuer {
	policy := deps.Policy
	if policy == nil {
		policy = DefaultAccessSnapshotPolicy()
	}
	signer := deps.Signer
	if signer == nil {
		signer = DefaultAccessSnapshotSigner()
	}
	projector := deps.SoftwareSubscriptions
	if projector == nil && deps.Repositories.EntitlementGrants != nil {
		projector = NewSoftwareSubscriptionProjector(SoftwareSubscriptionProjectionRepositories{
			EntitlementGrants: deps.Repositories.EntitlementGrants,
			Cancellations:     deps.Repositories.Cancellations,
		}, nil)
	}
	return &accessSnapshotIssuer{repos: deps.Repositories, policy: policy, cloudQuotaPolicy: deps.CloudQuotaPolicy, signer: signer, softwareSubscriptions: projector}
}

func (i *accessSnapshotIssuer) Issue(ctx context.Context, input AccessSnapshotIssueInput) (*domain.AccessSnapshotV2, error) {
	if i == nil || i.signer == nil || !i.hasRequiredRepos() || strings.TrimSpace(input.UserID) == "" {
		return nil, ErrInvalidAccessSnapshot
	}
	user, err := i.repos.Users.GetByID(ctx, strings.TrimSpace(input.UserID))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	grants, err := i.repos.EntitlementGrants.ListByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	activeGrants := activeEntitlementGrants(grants, now)
	entitlements := entitlementMapFromGrants(activeGrants)
	trial := i.activeTrial(ctx, user, now)
	device, err := i.snapshotDevice(ctx, user, strings.TrimSpace(input.DeviceID))
	if err != nil {
		return nil, err
	}
	issuedAt := now
	expiresAt := issuedAt.Add(i.policy.TTL(ctx, user))
	offlineGraceUntil := expiresAt.Add(i.policy.OfflineGrace(ctx, user))
	license := licenseProjectionFromGrants(activeGrants, trial, now)
	license = i.enrichLicenseWithSoftwareSubscription(ctx, user, license)
	snapshot := domain.AccessSnapshotV2{
		Version: 2,
		User: domain.AccessSnapshotUserV2{
			ID:            user.ID,
			Email:         user.Email,
			DisplayName:   user.DisplayName,
			Status:        user.Status,
			EmailVerified: false,
		},
		License:           license,
		Device:            device,
		Entitlements:      entitlements,
		Features:          i.featureProjection(ctx, user, activeGrants, entitlements),
		Credits:           creditSnapshotWithRepo(ctx, i.repos.CreditAccounts, user.ID),
		IssuedAt:          issuedAt.Format(time.RFC3339),
		ExpiresAt:         expiresAt.Format(time.RFC3339),
		OfflineGraceUntil: offlineGraceUntil.Format(time.RFC3339),
		Source:            "billing_provider",
		SignatureKeyID:    i.signer.KeyID(),
		SignatureAlg:      i.signer.Algorithm(),
	}
	signature, err := i.signer.Sign(snapshot)
	if err != nil {
		return nil, err
	}
	snapshot.Signature = signature
	return &snapshot, nil
}

func (i *accessSnapshotIssuer) enrichLicenseWithSoftwareSubscription(ctx context.Context, user *domain.User, license domain.AccessSnapshotLicenseV2) domain.AccessSnapshotLicenseV2 {
	if license.CurrentPeriodEndsAt == "" {
		license.CurrentPeriodEndsAt = license.SubscriptionEndsAt
	}
	if i.softwareSubscriptions == nil || user == nil || license.State != AccessLicenseStateSubscription {
		return license
	}
	subscription, err := i.softwareSubscriptions.Project(ctx, user.ID)
	if err != nil || subscription.SKUCode != domain.SKUProOwnAIMonthly {
		return license
	}
	license.SubscriptionStatus = subscription.Status
	license.CancelAtPeriodEnd = subscription.CancelAtPeriodEnd
	if subscription.CurrentPeriodEndsAt != "" {
		license.CurrentPeriodEndsAt = subscription.CurrentPeriodEndsAt
	}
	return license
}

func (i *accessSnapshotIssuer) hasRequiredRepos() bool {
	return i.repos.Users != nil && i.repos.EntitlementGrants != nil
}

func (i *accessSnapshotIssuer) activeTrial(ctx context.Context, user *domain.User, now time.Time) *domain.TrialGrant {
	if i.repos.TrialGrants == nil || user == nil {
		return nil
	}
	trials, err := i.repos.TrialGrants.List(ctx, repository.TrialGrantQuery{UserID: user.ID, GrantType: domain.TrialGrantTypeProOwnAI})
	if err != nil {
		return nil
	}
	var selected *domain.TrialGrant
	for idx := range trials {
		trial := trials[idx]
		if trial.Status != domain.TrialGrantStatusIssued {
			continue
		}
		if !trial.StartsAt.IsZero() && trial.StartsAt.After(now) {
			continue
		}
		if trial.ExpiresAt != nil && !trial.ExpiresAt.After(now) {
			continue
		}
		if selected == nil || (trial.ExpiresAt != nil && selected.ExpiresAt != nil && trial.ExpiresAt.After(*selected.ExpiresAt)) {
			copy := trial
			selected = &copy
		}
	}
	return selected
}

func (i *accessSnapshotIssuer) snapshotDevice(ctx context.Context, user *domain.User, deviceID string) (domain.AccessSnapshotDeviceV2, error) {
	maxDevices := normalizeAccessMaxDevices(i.policy.MaxDevices(ctx, user))
	if i.repos.Devices == nil || user == nil {
		return deviceSnapshotProjection(nil, newAccessDeviceCapacity(0, maxDevices)), nil
	}
	activeDevices, err := i.repos.Devices.ListByUser(ctx, user.ID, domain.DeviceStatusActive)
	if err != nil {
		return domain.AccessSnapshotDeviceV2{}, err
	}
	capacity := newAccessDeviceCapacity(len(activeDevices), maxDevices)
	if deviceID != "" {
		device, err := i.repos.Devices.GetByUserAndDevice(ctx, user.ID, deviceID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				projection := deviceSnapshotProjection(nil, capacity)
				projection.DeviceID = deviceID
				projection.Status = "unknown"
				return projection, nil
			}
			return domain.AccessSnapshotDeviceV2{}, err
		}
		if device.Status == domain.DeviceStatusDisabled {
			return domain.AccessSnapshotDeviceV2{}, ErrAccessDeviceRevoked
		}
		return deviceSnapshotProjection(device, capacity), nil
	}
	if len(activeDevices) == 0 {
		return deviceSnapshotProjection(nil, capacity), nil
	}
	return deviceSnapshotProjection(&activeDevices[0], capacity), nil
}

func (i *accessSnapshotIssuer) featureProjection(ctx context.Context, user *domain.User, grants []domain.EntitlementGrant, entitlements map[string]bool) map[string]any {
	features := map[string]any{"ai.hosted.available": false}
	if entitlements[domain.EntitlementCloudStorage] {
		quotaBytes := int64(0)
		if i.cloudQuotaPolicy != nil {
			quota := i.cloudQuotaPolicy.Decide(ctx, CloudStorageQuotaInput{User: user, Grants: grants, Now: time.Now().UTC()})
			quotaBytes = quota.QuotaBytes
			features["cloud.storage.plan"] = quota.Plan
		} else {
			quotaBytes = i.policy.CloudStorageQuotaMB(ctx, user) * 1024 * 1024
			features["cloud.storage.plan"] = CloudStoragePlanCustom
		}
		features["cloud.storage.quota_mb"] = quotaBytes / (1024 * 1024)
	} else {
		features["cloud.storage.plan"] = CloudStoragePlanNone
		features["cloud.storage.quota_mb"] = int64(0)
	}
	return features
}

func activeEntitlementGrants(grants []domain.EntitlementGrant, now time.Time) []domain.EntitlementGrant {
	active := make([]domain.EntitlementGrant, 0, len(grants))
	for _, grant := range grants {
		if isGrantActive(grant, now) {
			active = append(active, grant)
		}
	}
	return active
}

func entitlementMapFromGrants(grants []domain.EntitlementGrant) map[string]bool {
	entitlements := make(map[string]bool)
	for _, grant := range grants {
		if IsCurrentAccessEntitlementID(grant.EntitlementID) {
			entitlements[grant.EntitlementID] = true
		}
	}
	return entitlements
}

func licenseProjectionFromGrants(activeGrants []domain.EntitlementGrant, trial *domain.TrialGrant, now time.Time) domain.AccessSnapshotLicenseV2 {
	license := domain.AccessSnapshotLicenseV2{State: AccessLicenseStateBasic, Plan: domain.PlanBasicOwnAI, AIMode: AccessAIModeBYOK}
	if !hasAdvancedSoftware(activeGrants) {
		return license
	}
	var subscriptionEnd *time.Time
	var graceEnd *time.Time
	var trialEnd *time.Time
	for _, grant := range activeGrants {
		if !isAdvancedSoftwareGrant(grant) {
			continue
		}
		switch grant.Source {
		case domain.GrantSourceFulfillment:
			if grant.ExpiresAt == nil {
				license.State = AccessLicenseStateLifetime
				license.Plan = domain.SKUProOwnAILifetime
				return license
			}
			end := grant.ExpiresAt.UTC()
			if subscriptionEnd == nil || end.After(*subscriptionEnd) {
				subscriptionEnd = &end
			}
		case domain.GrantSourceSubscriptionGrace:
			if grant.ExpiresAt != nil {
				end := grant.ExpiresAt.UTC()
				if graceEnd == nil || end.After(*graceEnd) {
					graceEnd = &end
				}
			}
		case domain.GrantSourceTrial:
			if grant.ExpiresAt != nil {
				end := grant.ExpiresAt.UTC()
				if trialEnd == nil || end.After(*trialEnd) {
					trialEnd = &end
				}
			}
		default:
			if grant.ExpiresAt == nil {
				license.State = AccessLicenseStateLifetime
				license.Plan = domain.SKUProOwnAILifetime
				return license
			}
		}
	}
	if subscriptionEnd != nil && subscriptionEnd.After(now) {
		license.State = AccessLicenseStateSubscription
		license.Plan = domain.SKUProOwnAIMonthly
		license.SubscriptionEndsAt = subscriptionEnd.Format(time.RFC3339)
		return license
	}
	if graceEnd != nil && graceEnd.After(now) {
		license.State = AccessLicenseStateGrace
		license.Plan = domain.SKUProOwnAIMonthly
		license.GraceUntil = graceEnd.Format(time.RFC3339)
		return license
	}
	if trial != nil && trial.ExpiresAt != nil && trial.ExpiresAt.After(now) {
		trialEnd = &[]time.Time{trial.ExpiresAt.UTC()}[0]
	}
	if trialEnd != nil && trialEnd.After(now) {
		license.State = AccessLicenseStateTrial
		license.Plan = AccessPlanProOwnAITrial
		license.TrialEndsAt = trialEnd.Format(time.RFC3339)
	}
	return license
}

func hasAdvancedSoftware(grants []domain.EntitlementGrant) bool {
	for _, grant := range grants {
		if isAdvancedSoftwareGrant(grant) {
			return true
		}
	}
	return false
}

func isAdvancedSoftwareGrant(grant domain.EntitlementGrant) bool {
	return IsCurrentAdvancedEntitlementID(grant.EntitlementID)
}

func deviceSnapshotProjection(device *domain.UserDevice, capacity AccessDeviceCapacity) domain.AccessSnapshotDeviceV2 {
	if device == nil {
		return domain.AccessSnapshotDeviceV2{
			MaxDevices:           capacity.MaxDevices,
			ActiveDeviceCount:    capacity.ActiveDeviceCount,
			RemainingDeviceSlots: capacity.RemainingDeviceSlots,
		}
	}
	return domain.AccessSnapshotDeviceV2{
		ID:                   device.ID,
		DeviceID:             device.DeviceID,
		Status:               device.Status,
		MaxDevices:           capacity.MaxDevices,
		ActiveDeviceCount:    capacity.ActiveDeviceCount,
		RemainingDeviceSlots: capacity.RemainingDeviceSlots,
	}
}

func canonicalAccessSnapshotPayload(snapshot domain.AccessSnapshotV2) ([]byte, error) {
	unsigned := snapshot
	unsigned.Signature = ""
	return json.Marshal(unsigned)
}

func parseEd25519PrivateKey(value string) (ed25519.PrivateKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrInvalidAccessSnapshot
	}
	raw, err := decodeRawBase64(value)
	if err != nil {
		return nil, ErrInvalidAccessSnapshot
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, ErrInvalidAccessSnapshot
	}
}

func decodeRawBase64(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func decodeRawBase64URL(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func normalizeAccessSnapshotPolicyConfig(config AccessSnapshotPolicyConfig) AccessSnapshotPolicyConfig {
	defaults := DefaultAccessSnapshotPolicyConfig()
	if config.TTLSeconds <= 0 {
		config.TTLSeconds = defaults.TTLSeconds
	}
	if config.OfflineGraceSeconds < 0 {
		config.OfflineGraceSeconds = defaults.OfflineGraceSeconds
	}
	if config.MaxDevices <= 0 {
		config.MaxDevices = defaults.MaxDevices
	}
	if config.CloudStorageQuotaMB < 0 {
		config.CloudStorageQuotaMB = defaults.CloudStorageQuotaMB
	}
	return config
}
