package observability

import (
	"context"

	"walnut-billing/internal/domain"
	"walnut-billing/internal/metrics"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"
)

type observedAuditService struct {
	next service.AuditService
}

func NewObservedAuditService(next service.AuditService) service.AuditService {
	if next == nil {
		return nil
	}
	return &observedAuditService{next: next}
}

func (s *observedAuditService) Record(ctx context.Context, entry *domain.AuditEntry) {
	if entry != nil && isAdminActionMetric(entry.Action) {
		metrics.RecordAdminAction(entry.Action, entry.Success)
	}
	s.next.Record(ctx, entry)
}

func (s *observedAuditService) Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error) {
	return s.next.Query(ctx, query)
}

func (s *observedAuditService) Stop() {
	if stopper, ok := s.next.(interface{ Stop() }); ok {
		stopper.Stop()
	}
}

func isAdminActionMetric(action string) bool {
	switch action {
	case domain.AuditActionConfigUpdate,
		domain.AuditActionAdminQuery,
		domain.AuditActionRegistrationReview,
		domain.AuditActionAccessDeviceRevoke,
		domain.AuditActionEntitlementGrant,
		domain.AuditActionCreditGrant,
		domain.AuditActionCreditExpire,
		domain.AuditActionPaymentRiskResolve:
		return true
	default:
		return false
	}
}
