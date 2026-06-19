package service

import (
	"context"
	"log/slog"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"sync"
)

// AuditService handles recording and querying audit logs.
// It uses an async channel writer to avoid blocking business logic.
type AuditService interface {
	// Record logs an audit entry asynchronously.
	Record(ctx context.Context, entry *domain.AuditEntry)

	// Query returns audit entries matching the criteria.
	Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error)
}

type auditService struct {
	repo  repository.AuditRepository
	ch    chan *domain.AuditEntry
	wg    sync.WaitGroup
	done  chan struct{}
	log   *slog.Logger
}

// NewAuditService creates a new audit service with an async writer.
// The writer runs in a background goroutine and drains the channel.
func NewAuditService(repo repository.AuditRepository, bufferSize int, logger *slog.Logger) AuditService {
	svc := &auditService{
		repo: repo,
		ch:   make(chan *domain.AuditEntry, bufferSize),
		done: make(chan struct{}),
		log:  logger,
	}

	svc.wg.Add(1)
	go svc.writerLoop()

	return svc
}

// Record sends an audit entry to the async writer channel.
// It drops the entry if the channel is full (fire-and-forget semantics).
func (s *auditService) Record(ctx context.Context, entry *domain.AuditEntry) {
	if entry == nil {
		return
	}
	select {
	case s.ch <- entry:
		// Queued successfully
	default:
		s.log.Warn("audit log channel full, dropping entry",
			"action", entry.Action,
			"target", entry.Target,
		)
	}
}

// Query returns audit entries matching the query.
func (s *auditService) Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error) {
	total, err := s.repo.Count(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	entries, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	return entries, total, nil
}

// writerLoop drains the audit channel and persists entries to the repository.
func (s *auditService) writerLoop() {
	defer s.wg.Done()
	for {
		select {
		case entry, ok := <-s.ch:
			if !ok {
				return
			}
			if err := s.repo.Create(context.Background(), entry); err != nil {
				s.log.Error("failed to write audit log",
					"error", err,
					"action", entry.Action,
				)
			}
		case <-s.done:
			// Drain remaining entries before exiting
			for {
				select {
				case entry, ok := <-s.ch:
					if !ok {
						return
					}
					if err := s.repo.Create(context.Background(), entry); err != nil {
						s.log.Error("failed to write audit log during shutdown", "error", err)
					}
				default:
					return
				}
			}
		}
	}
}

// Stop gracefully shuts down the audit writer.
func (s *auditService) Stop() {
	close(s.done)
	s.wg.Wait()
}
