package service

import (
	"context"
	"time"
	"walnut-billing/internal/repository"
)

const (
	defaultAdminAuditLimit = 50
	maxAdminAuditLimit     = 200
)

type AdminAuditService interface {
	ListLogs(ctx context.Context, query AdminAuditQuery) (*AdminAuditLogList, error)
}

type AdminAuditQuery struct {
	Action    string
	Actor     string
	Target    string
	Success   *bool
	StartTime time.Time
	EndTime   time.Time
	Limit     int
	Offset    int
}

type AdminAuditLogList struct {
	Total int64           `json:"total"`
	Logs  []AdminAuditLog `json:"logs"`
}

type AdminAuditLog struct {
	ID        uint                 `json:"id"`
	Timestamp string               `json:"timestamp"`
	Actor     AdminActorProjection `json:"actor"`
	Action    string               `json:"action"`
	Target    string               `json:"target"`
	Details   string               `json:"details"`
	IPAddress string               `json:"ip_address"`
	Success   bool                 `json:"success"`
}

type adminAuditServiceImpl struct {
	audit   AuditService
	privacy AdminPrivacyProjector
}

func NewAdminAuditService(audit AuditService, privacy AdminPrivacyProjector) AdminAuditService {
	return &adminAuditServiceImpl{audit: audit, privacy: privacy}
}

func (s *adminAuditServiceImpl) ListLogs(ctx context.Context, query AdminAuditQuery) (*AdminAuditLogList, error) {
	if s == nil || s.audit == nil {
		return &AdminAuditLogList{}, nil
	}
	repoQuery := repository.AuditQuery{
		Action:    query.Action,
		Actor:     query.Actor,
		Target:    query.Target,
		Success:   query.Success,
		StartTime: query.StartTime,
		EndTime:   query.EndTime,
		Limit:     normalizeAdminAuditLimit(query.Limit),
		Offset:    maxInt(query.Offset, 0),
	}
	entries, total, err := s.audit.Query(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	logs := make([]AdminAuditLog, 0, len(entries))
	for _, entry := range entries {
		logs = append(logs, AdminAuditLog{
			ID:        entry.ID,
			Timestamp: formatTime(entry.Timestamp),
			Actor:     s.privacy.ProjectActor(entry.Actor),
			Action:    entry.Action,
			Target:    s.privacy.RedactIdentifier(entry.Target),
			Details:   s.privacy.RedactFreeText(entry.Details),
			IPAddress: entry.IPAddress,
			Success:   entry.Success,
		})
	}
	return &AdminAuditLogList{Total: total, Logs: logs}, nil
}

func normalizeAdminAuditLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminAuditLimit
	}
	if limit > maxAdminAuditLimit {
		return maxAdminAuditLimit
	}
	return limit
}
