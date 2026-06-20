package service

import (
	"context"
	"errors"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAdminTestScenarioResetRepo struct {
	query  repository.AdminTestScenarioResetQuery
	record *repository.AdminTestScenarioResetRecord
	err    error
}

func (f *fakeAdminTestScenarioResetRepo) ResetUserControlPlane(ctx context.Context, query repository.AdminTestScenarioResetQuery) (*repository.AdminTestScenarioResetRecord, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return f.record, nil
}

func TestAdminTestScenarioResetServiceRequiresNonProductionPolicy(t *testing.T) {
	svc := NewAdminTestScenarioResetService(&fakeAdminTestScenarioResetRepo{}, ServerEnvAdminTestScenarioResetPolicy{Env: "prod"}, NewAdminPrivacyProjector())
	_, err := svc.Reset(context.Background(), AdminTestScenarioResetInput{Email: "writer@example.com"})
	if !errors.Is(err, ErrAdminTestScenarioResetUnavailable) {
		t.Fatalf("expected production reset to be unavailable, got %v", err)
	}
}

func TestAdminTestScenarioResetServiceNormalizesInputAndRedactsEmail(t *testing.T) {
	repo := &fakeAdminTestScenarioResetRepo{
		record: &repository.AdminTestScenarioResetRecord{
			Scenario: AdminTestScenarioUserControlPlane,
			User:     &domain.User{ID: "usr_1", Email: "writer@example.com"},
			AffectedCounts: map[string]int64{
				"users":  1,
				"orders": 2,
			},
		},
	}
	svc := NewAdminTestScenarioResetService(repo, ServerEnvAdminTestScenarioResetPolicy{Env: "dev"}, NewAdminPrivacyProjector())

	result, err := svc.Reset(context.Background(), AdminTestScenarioResetInput{
		Scenario: "user-control-plane",
		Email:    " Writer@Example.COM ",
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if repo.query.Scenario != AdminTestScenarioUserControlPlane || repo.query.Email != "writer@example.com" || !repo.query.DryRun {
		t.Fatalf("unexpected repository query: %#v", repo.query)
	}
	if result.UserID != "usr_1" || result.EmailMasked == "" || result.EmailFingerprint == "" {
		t.Fatalf("expected redacted user projection, got %#v", result)
	}
	if result.AffectedCounts["orders"] != 2 {
		t.Fatalf("expected affected counts, got %#v", result.AffectedCounts)
	}
}

func TestAdminTestScenarioResetServiceMapsNotFound(t *testing.T) {
	svc := NewAdminTestScenarioResetService(
		&fakeAdminTestScenarioResetRepo{err: repository.ErrNotFound},
		ServerEnvAdminTestScenarioResetPolicy{Env: "test"},
		NewAdminPrivacyProjector(),
	)
	_, err := svc.Reset(context.Background(), AdminTestScenarioResetInput{Email: "missing@example.com"})
	if !errors.Is(err, ErrAdminTestScenarioNotFound) {
		t.Fatalf("expected not found mapping, got %v", err)
	}
}
