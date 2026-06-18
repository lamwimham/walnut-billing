package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidAccessLoginChallenge             = errors.New("invalid access login challenge")
	ErrAccessLoginChallengeExpired             = errors.New("access login challenge expired")
	ErrAccessLoginChallengeFailed              = errors.New("access login challenge verification failed")
	ErrAccessLoginChallengeDeliveryUnavailable = errors.New("access login challenge delivery unavailable")
)

type AccessLoginChallengeService interface {
	Create(ctx context.Context, input AccessLoginChallengeCreateInput) (*AccessLoginChallengeCreateResult, error)
	Verify(ctx context.Context, input AccessLoginChallengeVerifyInput) (*AccessSessionResult, error)
}

type AccessLoginChallengeCreateInput struct {
	Email          string
	DeviceID       string
	Source         string
	IdempotencyKey string
}

type AccessLoginChallengeVerifyInput struct {
	ChallengeID string
	Token       string
	DeviceID    string
	DisplayName string
	Source      string
}

type AccessLoginChallengeCreateResult struct {
	ChallengeID string    `json:"challenge_id"`
	Email       string    `json:"email"`
	DeviceID    string    `json:"device_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	Delivery    string    `json:"delivery"`
	DevToken    string    `json:"dev_token,omitempty"`
}

type AccessLoginChallengeDependencies struct {
	Challenges     repository.AccessLoginChallengeRepository
	AccessSession  AccessSessionService
	TokenGenerator AccessLoginTokenGenerator
	TokenHasher    AccessLoginTokenHasher
	Delivery       AccessLoginChallengeDelivery
	Policy         AccessLoginChallengePolicy
	Now            func() time.Time
}

type AccessLoginTokenGenerator interface {
	Generate(ctx context.Context) (string, error)
}

type AccessLoginTokenHasher interface {
	Hash(token string) string
	Verify(token string, tokenHash string) bool
}

type AccessLoginChallengeDelivery interface {
	Deliver(ctx context.Context, challenge *domain.AccessLoginChallenge, token string) (AccessLoginChallengeDeliveryResult, error)
}

type AccessLoginChallengeDeliveryAvailability interface {
	EnsureAvailable(ctx context.Context) error
}

type AccessLoginChallengeDeliveryResult struct {
	Channel  string
	DevToken string
}

type AccessLoginChallengePolicy interface {
	TTL(ctx context.Context, input AccessLoginChallengeCreateInput) time.Duration
	MaxAttempts(ctx context.Context, input AccessLoginChallengeCreateInput) int
}

type AccessLoginChallengePolicyConfig struct {
	TTLSeconds  int
	MaxAttempts int
}

type configurableAccessLoginChallengePolicy struct {
	config AccessLoginChallengePolicyConfig
}

func DefaultAccessLoginChallengePolicyConfig() AccessLoginChallengePolicyConfig {
	return AccessLoginChallengePolicyConfig{TTLSeconds: 10 * 60, MaxAttempts: 5}
}

func NewConfigurableAccessLoginChallengePolicy(config AccessLoginChallengePolicyConfig) AccessLoginChallengePolicy {
	defaults := DefaultAccessLoginChallengePolicyConfig()
	if config.TTLSeconds <= 0 {
		config.TTLSeconds = defaults.TTLSeconds
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = defaults.MaxAttempts
	}
	return &configurableAccessLoginChallengePolicy{config: config}
}

func (p *configurableAccessLoginChallengePolicy) TTL(ctx context.Context, input AccessLoginChallengeCreateInput) time.Duration {
	_ = ctx
	_ = input
	return time.Duration(p.config.TTLSeconds) * time.Second
}

func (p *configurableAccessLoginChallengePolicy) MaxAttempts(ctx context.Context, input AccessLoginChallengeCreateInput) int {
	_ = ctx
	_ = input
	return p.config.MaxAttempts
}

type accessLoginChallengeService struct {
	challenges     repository.AccessLoginChallengeRepository
	accessSession  AccessSessionService
	tokenGenerator AccessLoginTokenGenerator
	tokenHasher    AccessLoginTokenHasher
	delivery       AccessLoginChallengeDelivery
	policy         AccessLoginChallengePolicy
	now            func() time.Time
}

func NewAccessLoginChallengeService(deps AccessLoginChallengeDependencies) AccessLoginChallengeService {
	policy := deps.Policy
	if policy == nil {
		policy = NewConfigurableAccessLoginChallengePolicy(DefaultAccessLoginChallengePolicyConfig())
	}
	generator := deps.TokenGenerator
	if generator == nil {
		generator = NumericAccessLoginTokenGenerator{Digits: 6}
	}
	hasher := deps.TokenHasher
	if hasher == nil {
		hasher = HMACAccessLoginTokenHasher{Secret: "walnut-dev-login-challenge-secret"}
	}
	delivery := deps.Delivery
	if delivery == nil {
		delivery = DevAccessLoginChallengeDelivery{}
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &accessLoginChallengeService{
		challenges:     deps.Challenges,
		accessSession:  deps.AccessSession,
		tokenGenerator: generator,
		tokenHasher:    hasher,
		delivery:       delivery,
		policy:         policy,
		now:            now,
	}
}

func (s *accessLoginChallengeService) Create(ctx context.Context, input AccessLoginChallengeCreateInput) (*AccessLoginChallengeCreateResult, error) {
	if s == nil || s.challenges == nil || s.tokenGenerator == nil || s.tokenHasher == nil || s.delivery == nil || s.policy == nil {
		return nil, ErrInvalidAccessLoginChallenge
	}
	input.Email = normalizeEmail(input.Email)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.Source = defaultString(strings.TrimSpace(input.Source), "desktop")
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Email == "" || input.DeviceID == "" {
		return nil, ErrInvalidAccessLoginChallenge
	}
	if err := ensureAccessLoginChallengeDeliveryAvailable(ctx, s.delivery); err != nil {
		return nil, err
	}
	if input.IdempotencyKey != "" {
		if existing, err := s.challenges.GetByIdempotencyKey(ctx, input.IdempotencyKey); err == nil {
			return s.replayCreateResult(ctx, existing)
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	token, err := s.tokenGenerator.Generate(ctx)
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	challengeID, err := generateEntityID("alc_")
	if err != nil {
		return nil, err
	}
	challenge := &domain.AccessLoginChallenge{
		ID:             challengeID,
		Email:          input.Email,
		DeviceID:       input.DeviceID,
		TokenHash:      s.tokenHasher.Hash(token),
		Status:         domain.AccessLoginChallengeStatusPending,
		MaxAttempts:    s.policy.MaxAttempts(ctx, input),
		Source:         input.Source,
		IdempotencyKey: input.IdempotencyKey,
		ExpiresAt:      now.Add(s.policy.TTL(ctx, input)).UTC(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if challenge.IdempotencyKey == "" {
		challenge.IdempotencyKey = stableAccessSessionKey("login_challenge", challenge.Email, challenge.DeviceID, challenge.ID)
	}
	if err := s.challenges.Create(ctx, challenge); err != nil {
		if existing, getErr := s.challenges.GetByIdempotencyKey(ctx, challenge.IdempotencyKey); getErr == nil {
			return s.replayCreateResult(ctx, existing)
		}
		return nil, err
	}
	delivery, err := s.delivery.Deliver(ctx, challenge, token)
	if err != nil {
		challenge.Status = domain.AccessLoginChallengeStatusExpired
		challenge.UpdatedAt = s.currentTime()
		_ = s.challenges.Update(ctx, challenge)
		return nil, err
	}
	return s.createResult(ctx, challenge, token, delivery)
}

func (s *accessLoginChallengeService) replayCreateResult(ctx context.Context, challenge *domain.AccessLoginChallenge) (*AccessLoginChallengeCreateResult, error) {
	if challenge == nil {
		return nil, ErrInvalidAccessLoginChallenge
	}
	if challenge.Status == domain.AccessLoginChallengeStatusExpired || (!challenge.ExpiresAt.IsZero() && !challenge.ExpiresAt.After(s.currentTime())) {
		return nil, ErrAccessLoginChallengeExpired
	}
	if challenge.Status != domain.AccessLoginChallengeStatusPending || challenge.ConsumedAt != nil {
		return nil, ErrAccessLoginChallengeFailed
	}
	return s.createResult(ctx, challenge, "", AccessLoginChallengeDeliveryResult{Channel: "idempotent_replay"})
}

func (s *accessLoginChallengeService) Verify(ctx context.Context, input AccessLoginChallengeVerifyInput) (*AccessSessionResult, error) {
	if s == nil || s.challenges == nil || s.accessSession == nil || s.tokenHasher == nil {
		return nil, ErrInvalidAccessLoginChallenge
	}
	input.ChallengeID = strings.TrimSpace(input.ChallengeID)
	input.Token = strings.TrimSpace(input.Token)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.Source = defaultString(strings.TrimSpace(input.Source), "desktop")
	if input.ChallengeID == "" || input.Token == "" || input.DeviceID == "" {
		return nil, ErrInvalidAccessLoginChallenge
	}
	challenge, err := s.challenges.GetByID(ctx, input.ChallengeID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrAccessLoginChallengeFailed
		}
		return nil, err
	}
	now := s.currentTime()
	if challenge.Status != domain.AccessLoginChallengeStatusPending || challenge.ConsumedAt != nil {
		return nil, ErrAccessLoginChallengeFailed
	}
	if !challenge.ExpiresAt.IsZero() && !challenge.ExpiresAt.After(now) {
		challenge.Status = domain.AccessLoginChallengeStatusExpired
		challenge.UpdatedAt = now
		_ = s.challenges.Update(ctx, challenge)
		return nil, ErrAccessLoginChallengeExpired
	}
	if strings.TrimSpace(challenge.DeviceID) != input.DeviceID {
		return nil, s.recordFailedAttempt(ctx, challenge, now)
	}
	if !s.tokenHasher.Verify(input.Token, challenge.TokenHash) {
		return nil, s.recordFailedAttempt(ctx, challenge, now)
	}
	consumedAt := now
	consumed, err := s.challenges.ConsumePending(ctx, challenge.ID, consumedAt)
	if err != nil {
		return nil, err
	}
	if !consumed {
		return nil, ErrAccessLoginChallengeFailed
	}
	challenge.UpdatedAt = now
	challenge.ConsumedAt = &consumedAt
	return s.accessSession.RegisterOrRestore(ctx, AccessSessionInput{
		Email:       challenge.Email,
		DisplayName: input.DisplayName,
		DeviceID:    input.DeviceID,
		Source:      input.Source,
		Note:        "login_challenge:" + challenge.ID,
	})
}

func (s *accessLoginChallengeService) createResult(ctx context.Context, challenge *domain.AccessLoginChallenge, token string, delivery AccessLoginChallengeDeliveryResult) (*AccessLoginChallengeCreateResult, error) {
	_ = ctx
	if challenge == nil {
		return nil, ErrInvalidAccessLoginChallenge
	}
	return &AccessLoginChallengeCreateResult{
		ChallengeID: challenge.ID,
		Email:       challenge.Email,
		DeviceID:    challenge.DeviceID,
		ExpiresAt:   challenge.ExpiresAt,
		Delivery:    defaultString(strings.TrimSpace(delivery.Channel), "email"),
		DevToken:    delivery.DevToken,
	}, nil
}

func (s *accessLoginChallengeService) recordFailedAttempt(ctx context.Context, challenge *domain.AccessLoginChallenge, now time.Time) error {
	challenge.Attempts++
	challenge.UpdatedAt = now
	if challenge.MaxAttempts > 0 && challenge.Attempts >= challenge.MaxAttempts {
		challenge.Status = domain.AccessLoginChallengeStatusExpired
	}
	if err := s.challenges.Update(ctx, challenge); err != nil {
		return err
	}
	return ErrAccessLoginChallengeFailed
}

func (s *accessLoginChallengeService) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func ensureAccessLoginChallengeDeliveryAvailable(ctx context.Context, delivery AccessLoginChallengeDelivery) error {
	if delivery == nil {
		return ErrAccessLoginChallengeDeliveryUnavailable
	}
	availability, ok := delivery.(AccessLoginChallengeDeliveryAvailability)
	if !ok {
		return nil
	}
	if err := availability.EnsureAvailable(ctx); err != nil {
		return err
	}
	return nil
}

type NumericAccessLoginTokenGenerator struct {
	Digits int
}

func (g NumericAccessLoginTokenGenerator) Generate(ctx context.Context) (string, error) {
	_ = ctx
	digits := g.Digits
	if digits <= 0 {
		digits = 6
	}
	limit := big.NewInt(1)
	for i := 0; i < digits; i++ {
		limit.Mul(limit, big.NewInt(10))
	}
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, value.Int64()), nil
}

type HMACAccessLoginTokenHasher struct {
	Secret string
}

func (h HMACAccessLoginTokenHasher) Hash(token string) string {
	secret := strings.TrimSpace(h.Secret)
	if secret == "" {
		secret = "walnut-dev-login-challenge-secret"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h HMACAccessLoginTokenHasher) Verify(token string, tokenHash string) bool {
	expected := h.Hash(token)
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(tokenHash)))
}

type DevAccessLoginChallengeDelivery struct{}

func (DevAccessLoginChallengeDelivery) EnsureAvailable(ctx context.Context) error {
	_ = ctx
	return nil
}

func (DevAccessLoginChallengeDelivery) Deliver(ctx context.Context, challenge *domain.AccessLoginChallenge, token string) (AccessLoginChallengeDeliveryResult, error) {
	_ = ctx
	_ = challenge
	return AccessLoginChallengeDeliveryResult{Channel: "dev", DevToken: token}, nil
}

type DisabledAccessLoginChallengeDelivery struct {
	Reason string
}

func (d DisabledAccessLoginChallengeDelivery) EnsureAvailable(ctx context.Context) error {
	_ = ctx
	return ErrAccessLoginChallengeDeliveryUnavailable
}

func (d DisabledAccessLoginChallengeDelivery) Deliver(ctx context.Context, challenge *domain.AccessLoginChallenge, token string) (AccessLoginChallengeDeliveryResult, error) {
	_ = ctx
	_ = challenge
	_ = token
	return AccessLoginChallengeDeliveryResult{Channel: defaultString(strings.TrimSpace(d.Reason), "disabled")}, ErrAccessLoginChallengeDeliveryUnavailable
}
