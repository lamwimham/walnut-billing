package gorm_repo

import (
	"context"
	"fmt"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

// AuditRepo implements repository.AuditRepository using GORM.
type AuditRepo struct {
	DB *gorm.DB
}

// Create inserts a new audit entry. Append-only by design.
func (r *AuditRepo) Create(ctx context.Context, entry *domain.AuditEntry) error {
	return r.DB.WithContext(ctx).Create(entry).Error
}

// List returns audit entries matching the query criteria.
func (r *AuditRepo) List(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, error) {
	var entries []domain.AuditEntry
	q := r.DB.WithContext(ctx).Model(&domain.AuditEntry{})

	q = applyAuditFilters(q, query)

	if err := q.Order("timestamp DESC").
		Limit(query.Limit).
		Offset(query.Offset).
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("query audit logs: %w", err)
	}
	return entries, nil
}

// Count returns the number of audit entries matching the query criteria.
func (r *AuditRepo) Count(ctx context.Context, query repository.AuditQuery) (int64, error) {
	var count int64
	q := r.DB.WithContext(ctx).Model(&domain.AuditEntry{})
	q = applyAuditFilters(q, query)

	if err := q.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count audit logs: %w", err)
	}
	return count, nil
}

// applyAuditFilters applies common filters to the GORM query.
func applyAuditFilters(q *gorm.DB, query repository.AuditQuery) *gorm.DB {
	if query.Action != "" {
		q = q.Where("action = ?", query.Action)
	}
	if query.Actor != "" {
		q = q.Where("actor LIKE ?", "%"+query.Actor+"%")
	}
	if query.Target != "" {
		q = q.Where("target LIKE ?", "%"+query.Target+"%")
	}
	if query.Success != nil {
		q = q.Where("success = ?", *query.Success)
	}
	if !query.StartTime.IsZero() {
		q = q.Where("timestamp >= ?", query.StartTime)
	}
	if !query.EndTime.IsZero() {
		q = q.Where("timestamp <= ?", query.EndTime)
	}
	return q
}
