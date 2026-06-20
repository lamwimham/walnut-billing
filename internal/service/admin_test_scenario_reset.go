package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"walnut-billing/internal/repository"
)

const (
	AdminTestScenarioUserControlPlane = "user_control_plane"
)

var (
	ErrAdminTestScenarioResetUnavailable = errors.New("admin test scenario reset is unavailable")
	ErrInvalidAdminTestScenarioReset     = errors.New("invalid admin test scenario reset")
	ErrAdminTestScenarioNotFound         = errors.New("admin test scenario not found")
)

type AdminTestScenarioResetInput struct {
	Scenario string
	UserID   string
	Email    string
	DryRun   bool
}

type AdminTestScenarioResetResult struct {
	Scenario         string           `json:"scenario"`
	DryRun           bool             `json:"dry_run"`
	UserID           string           `json:"user_id,omitempty"`
	EmailMasked      string           `json:"email_masked,omitempty"`
	EmailFingerprint string           `json:"email_fingerprint,omitempty"`
	AffectedCounts   map[string]int64 `json:"affected_counts"`
}

type AdminTestScenarioResetPolicy interface {
	AllowReset() error
}

type ServerEnvAdminTestScenarioResetPolicy struct {
	Env string
}

func (p ServerEnvAdminTestScenarioResetPolicy) AllowReset() error {
	if strings.EqualFold(strings.TrimSpace(p.Env), "prod") {
		return ErrAdminTestScenarioResetUnavailable
	}
	return nil
}

type AdminTestScenarioResetService interface {
	Reset(ctx context.Context, input AdminTestScenarioResetInput) (*AdminTestScenarioResetResult, error)
}

type adminTestScenarioResetService struct {
	repo    repository.AdminTestScenarioResetRepository
	policy  AdminTestScenarioResetPolicy
	privacy AdminPrivacyProjector
}

func NewAdminTestScenarioResetService(repo repository.AdminTestScenarioResetRepository, policy AdminTestScenarioResetPolicy, privacy AdminPrivacyProjector) AdminTestScenarioResetService {
	if policy == nil {
		policy = ServerEnvAdminTestScenarioResetPolicy{Env: "prod"}
	}
	return &adminTestScenarioResetService{repo: repo, policy: policy, privacy: privacy}
}

func (s *adminTestScenarioResetService) Reset(ctx context.Context, input AdminTestScenarioResetInput) (*AdminTestScenarioResetResult, error) {
	if s == nil || s.repo == nil {
		return nil, ErrAdminTestScenarioResetUnavailable
	}
	if err := s.policy.AllowReset(); err != nil {
		return nil, err
	}
	scenario := normalizeAdminTestScenario(input.Scenario)
	if scenario == "" {
		scenario = AdminTestScenarioUserControlPlane
	}
	if scenario != AdminTestScenarioUserControlPlane {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAdminTestScenarioReset, scenario)
	}
	userID := strings.TrimSpace(input.UserID)
	email := normalizeEmail(input.Email)
	if userID == "" && email == "" {
		return nil, ErrInvalidAdminTestScenarioReset
	}
	record, err := s.repo.ResetUserControlPlane(ctx, repository.AdminTestScenarioResetQuery{
		Scenario: scenario,
		UserID:   userID,
		Email:    email,
		DryRun:   input.DryRun,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrAdminTestScenarioNotFound
		}
		return nil, err
	}
	return s.projectResult(record, input.DryRun), nil
}

func (s *adminTestScenarioResetService) projectResult(record *repository.AdminTestScenarioResetRecord, dryRun bool) *AdminTestScenarioResetResult {
	if record == nil {
		return &AdminTestScenarioResetResult{DryRun: dryRun, AffectedCounts: map[string]int64{}}
	}
	result := &AdminTestScenarioResetResult{
		Scenario:       record.Scenario,
		DryRun:         dryRun,
		AffectedCounts: cloneAffectedCounts(record.AffectedCounts),
	}
	if record.User != nil {
		result.UserID = record.User.ID
		projection := s.privacy.ProjectEmail(record.User.Email)
		result.EmailMasked = projection.Masked
		result.EmailFingerprint = projection.Fingerprint
		return result
	}
	if record.Email != "" {
		projection := s.privacy.ProjectEmail(record.Email)
		result.EmailMasked = projection.Masked
		result.EmailFingerprint = projection.Fingerprint
	}
	return result
}

func normalizeAdminTestScenario(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func cloneAffectedCounts(counts map[string]int64) map[string]int64 {
	if counts == nil {
		return map[string]int64{}
	}
	result := make(map[string]int64, len(counts))
	for key, value := range counts {
		result[key] = value
	}
	return result
}
