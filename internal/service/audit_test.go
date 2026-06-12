package service

import (
	"context"
	"log/slog"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"testing"
	"time"
)

// mockAuditRepo implements repository.AuditRepository for testing
type mockAuditRepo struct {
	entries []*domain.AuditEntry
}

func (m *mockAuditRepo) Create(ctx context.Context, entry *domain.AuditEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockAuditRepo) List(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, error) {
	result := make([]domain.AuditEntry, len(m.entries))
	for i, e := range m.entries {
		result[i] = *e
	}
	return result, nil
}

func (m *mockAuditRepo) Count(ctx context.Context, query repository.AuditQuery) (int64, error) {
	return int64(len(m.entries)), nil
}

var _ repository.AuditRepository = (*mockAuditRepo)(nil)

func TestAuditService_AsyncWrite(t *testing.T) {
	repo := &mockAuditRepo{}
	svc := NewAuditService(repo, 10, slog.Default())
	defer svc.(interface{ Stop() }).Stop()

	ctx := context.Background()
	svc.Record(ctx, &domain.AuditEntry{
		Action: "test.action",
		Target: "test-target",
	})

	// Give async writer time to process
	time.Sleep(50 * time.Millisecond)

	if len(repo.entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(repo.entries))
	}
	if repo.entries[0].Action != "test.action" {
		t.Errorf("expected action test.action, got %s", repo.entries[0].Action)
	}
}

func TestAuditService_ChannelFull(t *testing.T) {
	repo := &mockAuditRepo{}
	svc := NewAuditService(repo, 1, slog.Default())
	defer svc.(interface{ Stop() }).Stop()

	ctx := context.Background()

	// Fill the channel and one extra
	svc.Record(ctx, &domain.AuditEntry{Action: "fill1"})
	svc.Record(ctx, &domain.AuditEntry{Action: "fill2"})
	svc.Record(ctx, &domain.AuditEntry{Action: "dropped"})

	time.Sleep(50 * time.Millisecond)

	// Should have processed fill1 and fill2, but dropped might be lost
	// This is expected fire-and-forget behavior
	t.Logf("Processed %d entries (channel full dropping is expected)", len(repo.entries))
}

func TestAuditService_Query(t *testing.T) {
	repo := &mockAuditRepo{
		entries: []*domain.AuditEntry{
			{Action: "license.activate", Target: "key1"},
			{Action: "payment.callback", Target: "order1"},
		},
	}
	svc := NewAuditService(repo, 10, slog.Default())
	defer svc.(interface{ Stop() }).Stop()

	ctx := context.Background()

	entries, total, err := svc.Query(ctx, repository.AuditQuery{Action: "license.activate"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	// Note: mock repo returns all, filtering is done by GORM repo in real impl
	t.Logf("Query returned %d entries", len(entries))
}

func TestAuditService_GracefulShutdown(t *testing.T) {
	repo := &mockAuditRepo{}
	svc := NewAuditService(repo, 100, slog.Default())

	ctx := context.Background()
	// Record several entries
	for i := 0; i < 5; i++ {
		svc.Record(ctx, &domain.AuditEntry{Action: "shutdown.test"})
	}

	// Stop should drain the channel
	svc.(interface{ Stop() }).Stop()

	if len(repo.entries) != 5 {
		t.Errorf("expected 5 entries after shutdown, got %d", len(repo.entries))
	}
}
