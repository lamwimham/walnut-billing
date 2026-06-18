package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockAccessLoginChallengeRepo struct {
	byID                 map[string]*domain.AccessLoginChallenge
	byKey                map[string]*domain.AccessLoginChallenge
	consumePendingResult *bool
}

func newMockAccessLoginChallengeRepo() *mockAccessLoginChallengeRepo {
	return &mockAccessLoginChallengeRepo{byID: map[string]*domain.AccessLoginChallenge{}, byKey: map[string]*domain.AccessLoginChallenge{}}
}

func (m *mockAccessLoginChallengeRepo) Create(ctx context.Context, challenge *domain.AccessLoginChallenge) error {
	m.byID[challenge.ID] = challenge
	m.byKey[challenge.IdempotencyKey] = challenge
	return nil
}
func (m *mockAccessLoginChallengeRepo) GetByID(ctx context.Context, id string) (*domain.AccessLoginChallenge, error) {
	challenge, ok := m.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return challenge, nil
}
func (m *mockAccessLoginChallengeRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.AccessLoginChallenge, error) {
	challenge, ok := m.byKey[key]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return challenge, nil
}
func (m *mockAccessLoginChallengeRepo) Update(ctx context.Context, challenge *domain.AccessLoginChallenge) error {
	m.byID[challenge.ID] = challenge
	m.byKey[challenge.IdempotencyKey] = challenge
	return nil
}
func (m *mockAccessLoginChallengeRepo) ConsumePending(ctx context.Context, id string, consumedAt time.Time) (bool, error) {
	if m.consumePendingResult != nil {
		return *m.consumePendingResult, nil
	}
	challenge, ok := m.byID[id]
	if !ok || challenge.Status != domain.AccessLoginChallengeStatusPending || challenge.ConsumedAt != nil {
		return false, nil
	}
	challenge.Status = domain.AccessLoginChallengeStatusConsumed
	challenge.UpdatedAt = consumedAt
	challenge.ConsumedAt = &consumedAt
	m.byID[challenge.ID] = challenge
	m.byKey[challenge.IdempotencyKey] = challenge
	return true, nil
}

type fixedLoginTokenGenerator struct{ token string }

func (g fixedLoginTokenGenerator) Generate(ctx context.Context) (string, error) { return g.token, nil }

type fakeAccessSessionService struct {
	input AccessSessionInput
	err   error
}

func (s *fakeAccessSessionService) RegisterOrRestore(ctx context.Context, input AccessSessionInput) (*AccessSessionResult, error) {
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	return &AccessSessionResult{User: &domain.User{ID: "usr_1", Email: input.Email}, Device: &domain.UserDevice{DeviceID: input.DeviceID}}, nil
}

type unavailableAccessLoginDelivery struct{}

func (unavailableAccessLoginDelivery) EnsureAvailable(ctx context.Context) error {
	_ = ctx
	return ErrAccessLoginChallengeDeliveryUnavailable
}

type failingAccessLoginDelivery struct{}

func (failingAccessLoginDelivery) Deliver(ctx context.Context, challenge *domain.AccessLoginChallenge, token string) (AccessLoginChallengeDeliveryResult, error) {
	_ = ctx
	_ = challenge
	_ = token
	return AccessLoginChallengeDeliveryResult{}, ErrAccessLoginChallengeDeliveryUnavailable
}

func (unavailableAccessLoginDelivery) Deliver(ctx context.Context, challenge *domain.AccessLoginChallenge, token string) (AccessLoginChallengeDeliveryResult, error) {
	_ = ctx
	_ = challenge
	_ = token
	return AccessLoginChallengeDeliveryResult{}, ErrAccessLoginChallengeDeliveryUnavailable
}

func TestAccessLoginChallengeService_CreateHashesTokenAndReturnsDevDelivery(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	hasher := HMACAccessLoginTokenHasher{Secret: "test-secret"}
	now := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    hasher,
		Policy:         NewConfigurableAccessLoginChallengePolicy(AccessLoginChallengePolicyConfig{TTLSeconds: 60, MaxAttempts: 2}),
		Now:            func() time.Time { return now },
	})
	result, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: " Writer@Example.COM ", DeviceID: "device-1", IdempotencyKey: "login:1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	challenge := repo.byID[result.ChallengeID]
	if challenge == nil || challenge.Email != "writer@example.com" || challenge.DeviceID != "device-1" {
		t.Fatalf("unexpected challenge: %#v", challenge)
	}
	if challenge.TokenHash == "123456" || !hasher.Verify("123456", challenge.TokenHash) {
		t.Fatalf("expected hashed token, got %q", challenge.TokenHash)
	}
	if result.DevToken != "123456" || result.Delivery != "dev" || !result.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected create result: %#v", result)
	}
}

func TestAccessLoginChallengeService_VerifyConsumesAndDelegatesToAccessSession(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	session := &fakeAccessSessionService{}
	hasher := HMACAccessLoginTokenHasher{Secret: "test-secret"}
	now := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  session,
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    hasher,
		Now:            func() time.Time { return now },
	})
	created, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	_, err = svc.Verify(context.Background(), AccessLoginChallengeVerifyInput{ChallengeID: created.ChallengeID, Token: "123456", DeviceID: "device-1", DisplayName: "Writer"})
	if err != nil {
		t.Fatalf("verify challenge: %v", err)
	}
	challenge := repo.byID[created.ChallengeID]
	if challenge.Status != domain.AccessLoginChallengeStatusConsumed || challenge.ConsumedAt == nil {
		t.Fatalf("expected consumed challenge, got %#v", challenge)
	}
	if session.input.Email != "writer@example.com" || session.input.DeviceID != "device-1" || session.input.DisplayName != "Writer" {
		t.Fatalf("expected access session delegation, got %#v", session.input)
	}
	_, err = svc.Verify(context.Background(), AccessLoginChallengeVerifyInput{ChallengeID: created.ChallengeID, Token: "123456", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessLoginChallengeFailed) {
		t.Fatalf("expected consumed challenge reuse failure, got %v", err)
	}
}

func TestAccessLoginChallengeService_VerifyRejectsConcurrentConsumption(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	session := &fakeAccessSessionService{}
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  session,
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
		Now:            func() time.Time { return time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC) },
	})
	created, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	consumeResult := false
	repo.consumePendingResult = &consumeResult
	_, err = svc.Verify(context.Background(), AccessLoginChallengeVerifyInput{ChallengeID: created.ChallengeID, Token: "123456", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessLoginChallengeFailed) {
		t.Fatalf("expected concurrent consumption failure, got %v", err)
	}
	if session.input.Email != "" {
		t.Fatalf("access session should not be called after failed consume: %#v", session.input)
	}
}

func TestAccessLoginChallengeService_CreateFailsWhenDeliveryUnavailable(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
		Delivery:       unavailableAccessLoginDelivery{},
	})
	_, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessLoginChallengeDeliveryUnavailable) {
		t.Fatalf("expected unavailable delivery error, got %v", err)
	}
	if len(repo.byID) != 0 {
		t.Fatalf("challenge should not be persisted without delivery, got %#v", repo.byID)
	}
}

func TestAccessLoginChallengeService_CreateExpiresChallengeWhenDeliveryFails(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
		Delivery:       failingAccessLoginDelivery{},
	})
	_, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessLoginChallengeDeliveryUnavailable) {
		t.Fatalf("expected delivery error, got %v", err)
	}
	if len(repo.byID) != 1 {
		t.Fatalf("expected persisted failed challenge for audit, got %#v", repo.byID)
	}
	for _, challenge := range repo.byID {
		if challenge.Status != domain.AccessLoginChallengeStatusExpired {
			t.Fatalf("expected failed delivery challenge to be expired, got %#v", challenge)
		}
	}
}

func TestAccessLoginChallengeService_CreateIdempotentReplayRejectsConsumedChallenge(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
	})
	created, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1", IdempotencyKey: "login:1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	repo.byID[created.ChallengeID].Status = domain.AccessLoginChallengeStatusConsumed
	_, err = svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1", IdempotencyKey: "login:1"})
	if !errors.Is(err, ErrAccessLoginChallengeFailed) {
		t.Fatalf("expected consumed replay failure, got %v", err)
	}
}

func TestAccessLoginChallengeService_VerifyWrongTokenExpiresAfterMaxAttempts(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
		Policy:         NewConfigurableAccessLoginChallengePolicy(AccessLoginChallengePolicyConfig{TTLSeconds: 60, MaxAttempts: 2}),
		Now:            func() time.Time { return time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC) },
	})
	created, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	for i := 0; i < 2; i++ {
		_, err = svc.Verify(context.Background(), AccessLoginChallengeVerifyInput{ChallengeID: created.ChallengeID, Token: "000000", DeviceID: "device-1"})
		if !errors.Is(err, ErrAccessLoginChallengeFailed) {
			t.Fatalf("expected failed attempt, got %v", err)
		}
	}
	challenge := repo.byID[created.ChallengeID]
	if challenge.Attempts != 2 || challenge.Status != domain.AccessLoginChallengeStatusExpired {
		t.Fatalf("expected expired after max attempts, got %#v", challenge)
	}
}

func TestAccessLoginChallengeService_VerifyExpiredChallenge(t *testing.T) {
	repo := newMockAccessLoginChallengeRepo()
	now := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	current := now
	svc := NewAccessLoginChallengeService(AccessLoginChallengeDependencies{
		Challenges:     repo,
		AccessSession:  &fakeAccessSessionService{},
		TokenGenerator: fixedLoginTokenGenerator{token: "123456"},
		TokenHasher:    HMACAccessLoginTokenHasher{Secret: "test-secret"},
		Policy:         NewConfigurableAccessLoginChallengePolicy(AccessLoginChallengePolicyConfig{TTLSeconds: 1, MaxAttempts: 2}),
		Now:            func() time.Time { return current },
	})
	created, err := svc.Create(context.Background(), AccessLoginChallengeCreateInput{Email: "writer@example.com", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	current = now.Add(2 * time.Second)
	_, err = svc.Verify(context.Background(), AccessLoginChallengeVerifyInput{ChallengeID: created.ChallengeID, Token: "123456", DeviceID: "device-1"})
	if !errors.Is(err, ErrAccessLoginChallengeExpired) {
		t.Fatalf("expected expired challenge, got %v", err)
	}
	if repo.byID[created.ChallengeID].Status != domain.AccessLoginChallengeStatusExpired {
		t.Fatalf("expected challenge status expired")
	}
}
