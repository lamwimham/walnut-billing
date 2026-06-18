package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAuditSource struct {
	query   repository.AuditQuery
	entries []domain.AuditEntry
	total   int64
}

func (f *fakeAuditSource) Record(ctx context.Context, entry *domain.AuditEntry) {}

func (f *fakeAuditSource) Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error) {
	f.query = query
	return f.entries, f.total, nil
}

func TestAdminAuditServiceRedactsEmailActorsAndFreeText(t *testing.T) {
	now := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	audit := &fakeAuditSource{total: 1, entries: []domain.AuditEntry{{
		ID:        7,
		Timestamp: now,
		Actor:     "Writer@Example.COM",
		Action:    domain.AuditActionRegistrationSubmit,
		Target:    "writer@example.com",
		Details:   "restore Writer@Example.COM from desktop",
		IPAddress: "127.0.0.1",
		Success:   true,
	}}}
	svc := NewAdminAuditService(audit, NewAdminPrivacyProjector())

	result, err := svc.ListLogs(context.Background(), AdminAuditQuery{Limit: 999, Offset: -5})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if audit.query.Limit != maxAdminAuditLimit || audit.query.Offset != 0 {
		t.Fatalf("expected normalized query, got %#v", audit.query)
	}
	if result.Total != 1 || len(result.Logs) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	log := result.Logs[0]
	if log.Actor.Type != "email" || log.Actor.Masked != "wr**er@example.com" || log.Actor.Fingerprint == "" || log.Actor.Domain != "example.com" {
		t.Fatalf("expected redacted email actor, got %#v", log.Actor)
	}
	payload, _ := json.Marshal(result)
	if strings.Contains(string(payload), "writer@example.com") || strings.Contains(string(payload), "Writer@Example.COM") {
		t.Fatalf("admin audit projection leaked raw email: %s", payload)
	}
}

func TestAdminAuditServiceKeepsStableUserActor(t *testing.T) {
	audit := &fakeAuditSource{total: 1, entries: []domain.AuditEntry{{Actor: "usr_123", Action: "x", Target: "target", Success: true}}}
	svc := NewAdminAuditService(audit, NewAdminPrivacyProjector())

	result, err := svc.ListLogs(context.Background(), AdminAuditQuery{})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if result.Logs[0].Actor.Type != "user" || result.Logs[0].Actor.ID != "usr_123" {
		t.Fatalf("expected stable user actor, got %#v", result.Logs[0].Actor)
	}
}
