package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ErrAccessLoginChallengeRateLimited         = errors.New("access login challenge rate limited")
)

const (
	AccessLoginChallengeAbuseReasonRateLimited  = "rate_limited"
	AccessLoginChallengeAbuseReasonMaxAttempts  = "max_attempts_exceeded"
	accessLoginChallengeFailureReasonWrongToken = "wrong_token"
	accessLoginChallengeFailureReasonDevice     = "device_mismatch"
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
	ClientIP       string
	UserAgent      string
}

type AccessLoginChallengeVerifyInput struct {
	ChallengeID string
	Token       string
	DeviceID    string
	DisplayName string
	Source      string
	ClientIP    string
	UserAgent   string
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
	AbuseObserver  AccessLoginChallengeAbuseObserver
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
	RateLimit(ctx context.Context, input AccessLoginChallengeCreateInput) AccessLoginChallengeRateLimit
}

type AccessLoginChallengeRateLimit struct {
	Window      time.Duration
	MaxPerEmail int
	MaxPerIP    int
}

type AccessLoginChallengeAbuseObserver interface {
	ObserveAccessLoginChallengeAbuse(ctx context.Context, event AccessLoginChallengeAbuseEvent)
}

type AccessLoginChallengeAbuseEvent struct {
	ChallengeID         string `json:"challenge_id,omitempty"`
	EmailFingerprint    string `json:"email_fingerprint,omitempty"`
	DeviceIDFingerprint string `json:"device_id_fingerprint,omitempty"`
	ClientIPHash        string `json:"client_ip_hash,omitempty"`
	UserAgentHash       string `json:"user_agent_hash,omitempty"`
	Reason              string `json:"reason"`
	Attempts            int    `json:"attempts,omitempty"`
	MaxAttempts         int    `json:"max_attempts,omitempty"`
	LimitWindowSeconds  int64  `json:"limit_window_seconds,omitempty"`
	Limit               int    `json:"limit,omitempty"`
}

type AccessLoginChallengePolicyConfig struct {
	TTLSeconds             int
	MaxAttempts            int
	RateLimitWindowSeconds int
	MaxCreatesPerEmail     int
	MaxCreatesPerIP        int
}

type configurableAccessLoginChallengePolicy struct {
	config AccessLoginChallengePolicyConfig
}

func DefaultAccessLoginChallengePolicyConfig() AccessLoginChallengePolicyConfig {
	return AccessLoginChallengePolicyConfig{
		TTLSeconds:             10 * 60,
		MaxAttempts:            5,
		RateLimitWindowSeconds: 10 * 60,
		MaxCreatesPerEmail:     5,
		MaxCreatesPerIP:        20,
	}
}

func NewConfigurableAccessLoginChallengePolicy(config AccessLoginChallengePolicyConfig) AccessLoginChallengePolicy {
	defaults := DefaultAccessLoginChallengePolicyConfig()
	if config.TTLSeconds <= 0 {
		config.TTLSeconds = defaults.TTLSeconds
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = defaults.MaxAttempts
	}
	if config.RateLimitWindowSeconds <= 0 {
		config.RateLimitWindowSeconds = defaults.RateLimitWindowSeconds
	}
	if config.MaxCreatesPerEmail <= 0 {
		config.MaxCreatesPerEmail = defaults.MaxCreatesPerEmail
	}
	if config.MaxCreatesPerIP <= 0 {
		config.MaxCreatesPerIP = defaults.MaxCreatesPerIP
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

func (p *configurableAccessLoginChallengePolicy) RateLimit(ctx context.Context, input AccessLoginChallengeCreateInput) AccessLoginChallengeRateLimit {
	_ = ctx
	_ = input
	return AccessLoginChallengeRateLimit{
		Window:      time.Duration(p.config.RateLimitWindowSeconds) * time.Second,
		MaxPerEmail: p.config.MaxCreatesPerEmail,
		MaxPerIP:    p.config.MaxCreatesPerIP,
	}
}

type accessLoginChallengeService struct {
	challenges     repository.AccessLoginChallengeRepository
	accessSession  AccessSessionService
	tokenGenerator AccessLoginTokenGenerator
	tokenHasher    AccessLoginTokenHasher
	delivery       AccessLoginChallengeDelivery
	policy         AccessLoginChallengePolicy
	abuseObserver  AccessLoginChallengeAbuseObserver
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
		abuseObserver:  deps.AbuseObserver,
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
	input.ClientIP = strings.TrimSpace(input.ClientIP)
	input.UserAgent = strings.TrimSpace(input.UserAgent)
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
	now := s.currentTime()
	metadata := newAccessLoginChallengeRequestMetadata(input.Email, input.DeviceID, input.ClientIP, input.UserAgent, s.tokenHasher)
	if err := s.enforceCreateRateLimit(ctx, input, metadata, now); err != nil {
		return nil, err
	}
	token, err := s.tokenGenerator.Generate(ctx)
	if err != nil {
		return nil, err
	}
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
		ClientIPHash:   metadata.ClientIPHash,
		UserAgentHash:  metadata.UserAgentHash,
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
	input.ClientIP = strings.TrimSpace(input.ClientIP)
	input.UserAgent = strings.TrimSpace(input.UserAgent)
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
		challenge.FailureReason = "expired"
		challenge.UpdatedAt = now
		_ = s.challenges.Update(ctx, challenge)
		return nil, ErrAccessLoginChallengeExpired
	}
	if strings.TrimSpace(challenge.DeviceID) != input.DeviceID {
		return nil, s.recordFailedAttempt(ctx, challenge, now, accessLoginChallengeFailureReasonDevice)
	}
	if !s.tokenHasher.Verify(input.Token, challenge.TokenHash) {
		return nil, s.recordFailedAttempt(ctx, challenge, now, accessLoginChallengeFailureReasonWrongToken)
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

func (s *accessLoginChallengeService) enforceCreateRateLimit(ctx context.Context, input AccessLoginChallengeCreateInput, metadata accessLoginChallengeRequestMetadata, now time.Time) error {
	limit := s.policy.RateLimit(ctx, input)
	if limit.Window <= 0 {
		return nil
	}
	createdAfter := now.Add(-limit.Window)
	if limit.MaxPerEmail > 0 {
		count, err := s.challenges.Count(ctx, repository.AccessLoginChallengeQuery{Email: input.Email, CreatedAfter: createdAfter})
		if err != nil {
			return err
		}
		if count >= int64(limit.MaxPerEmail) {
			s.observeAbuse(ctx, AccessLoginChallengeAbuseEvent{
				EmailFingerprint:    metadata.EmailFingerprint,
				DeviceIDFingerprint: metadata.DeviceIDFingerprint,
				ClientIPHash:        shortFingerprint(metadata.ClientIPHash),
				UserAgentHash:       shortFingerprint(metadata.UserAgentHash),
				Reason:              AccessLoginChallengeAbuseReasonRateLimited,
				LimitWindowSeconds:  int64(limit.Window.Seconds()),
				Limit:               limit.MaxPerEmail,
			})
			return ErrAccessLoginChallengeRateLimited
		}
	}
	if limit.MaxPerIP > 0 && metadata.ClientIPHash != "" {
		count, err := s.challenges.Count(ctx, repository.AccessLoginChallengeQuery{ClientIPHash: metadata.ClientIPHash, CreatedAfter: createdAfter})
		if err != nil {
			return err
		}
		if count >= int64(limit.MaxPerIP) {
			s.observeAbuse(ctx, AccessLoginChallengeAbuseEvent{
				EmailFingerprint:    metadata.EmailFingerprint,
				DeviceIDFingerprint: metadata.DeviceIDFingerprint,
				ClientIPHash:        shortFingerprint(metadata.ClientIPHash),
				UserAgentHash:       shortFingerprint(metadata.UserAgentHash),
				Reason:              AccessLoginChallengeAbuseReasonRateLimited,
				LimitWindowSeconds:  int64(limit.Window.Seconds()),
				Limit:               limit.MaxPerIP,
			})
			return ErrAccessLoginChallengeRateLimited
		}
	}
	return nil
}

func (s *accessLoginChallengeService) recordFailedAttempt(ctx context.Context, challenge *domain.AccessLoginChallenge, now time.Time, reason string) error {
	challenge.Attempts++
	challenge.FailureReason = strings.TrimSpace(reason)
	challenge.UpdatedAt = now
	var abuseEvent *AccessLoginChallengeAbuseEvent
	if challenge.MaxAttempts > 0 && challenge.Attempts >= challenge.MaxAttempts {
		challenge.Status = domain.AccessLoginChallengeStatusExpired
		abuseEvent = &AccessLoginChallengeAbuseEvent{
			ChallengeID:         challenge.ID,
			EmailFingerprint:    emailFingerprint(challenge.Email),
			DeviceIDFingerprint: deviceFingerprint(challenge.DeviceID),
			ClientIPHash:        shortFingerprint(challenge.ClientIPHash),
			UserAgentHash:       shortFingerprint(challenge.UserAgentHash),
			Reason:              AccessLoginChallengeAbuseReasonMaxAttempts,
			Attempts:            challenge.Attempts,
			MaxAttempts:         challenge.MaxAttempts,
		}
	}
	if err := s.challenges.Update(ctx, challenge); err != nil {
		return err
	}
	if abuseEvent != nil {
		s.observeAbuse(ctx, *abuseEvent)
	}
	return ErrAccessLoginChallengeFailed
}

func (s *accessLoginChallengeService) observeAbuse(ctx context.Context, event AccessLoginChallengeAbuseEvent) {
	if s == nil || s.abuseObserver == nil {
		return
	}
	s.abuseObserver.ObserveAccessLoginChallengeAbuse(ctx, event)
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

type accessLoginChallengeRequestMetadata struct {
	EmailFingerprint    string
	DeviceIDFingerprint string
	ClientIPHash        string
	UserAgentHash       string
}

func newAccessLoginChallengeRequestMetadata(email, deviceID, clientIP, userAgent string, hasher AccessLoginTokenHasher) accessLoginChallengeRequestMetadata {
	return accessLoginChallengeRequestMetadata{
		EmailFingerprint:    emailFingerprint(email),
		DeviceIDFingerprint: deviceFingerprint(deviceID),
		ClientIPHash:        accessLoginChallengeHash("client_ip", clientIP, hasher),
		UserAgentHash:       accessLoginChallengeHash("user_agent", userAgent, hasher),
	}
}

func accessLoginChallengeHash(scope, value string, hasher AccessLoginTokenHasher) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	scopedValue := "metadata:" + strings.TrimSpace(scope) + ":" + value
	if hasher != nil {
		return hasher.Hash(scopedValue)
	}
	digest := sha256.Sum256([]byte("walnut-access-login-challenge-v1:" + strings.TrimSpace(scope) + ":" + value))
	return hex.EncodeToString(digest[:])
}

func shortFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

type accessLoginChallengeAuditObserver struct {
	audit AuditService
}

func NewAccessLoginChallengeAuditObserver(audit AuditService) AccessLoginChallengeAbuseObserver {
	return accessLoginChallengeAuditObserver{audit: audit}
}

func (o accessLoginChallengeAuditObserver) ObserveAccessLoginChallengeAbuse(ctx context.Context, event AccessLoginChallengeAbuseEvent) {
	if o.audit == nil {
		return
	}
	details, _ := json.Marshal(event)
	o.audit.Record(ctx, &domain.AuditEntry{
		Timestamp: time.Now().UTC(),
		Actor:     defaultString(event.EmailFingerprint, "unknown"),
		Action:    domain.AuditActionAccessLoginChallengeAbuse,
		Target:    defaultString(event.ChallengeID, "access_login_challenge"),
		Success:   false,
		Details:   string(details),
		IPAddress: defaultString(event.ClientIPHash, "redacted"),
	})
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
