package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

// mockAuditWithData returns an audit service with pre-populated data for testing
type mockAuditWithData struct {
	entries []domain.AuditEntry
}

func (m *mockAuditWithData) Record(ctx context.Context, entry *domain.AuditEntry) {
	m.entries = append(m.entries, *entry)
}

func (m *mockAuditWithData) Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error) {
	result := m.entries
	total := int64(len(result))

	if query.Action != "" {
		var filtered []domain.AuditEntry
		for _, e := range result {
			if e.Action == query.Action {
				filtered = append(filtered, e)
			}
		}
		result = filtered
		total = int64(len(result))
	}

	if query.Limit > 0 {
		end := query.Offset + query.Limit
		if end > len(result) {
			end = len(result)
		}
		result = result[query.Offset:end]
	}

	return result, total, nil
}

var _ service.AuditService = (*mockAuditWithData)(nil)

func TestAdminHandler_GetAuditLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAudit := &mockAuditWithData{
		entries: []domain.AuditEntry{
			{Action: "license.activate", Target: "key1", Success: true, Timestamp: time.Now().Add(-1 * time.Hour)},
			{Action: "payment.callback", Target: "order1", Success: true, Timestamp: time.Now()},
		},
	}

	adminHandler := NewAdminHandler(nil, mockAudit)
	r := gin.New()
	r.GET("/admin/audit", adminHandler.GetAuditLogs)

	req, _ := http.NewRequest("GET", "/admin/audit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if int(resp["total"].(float64)) != 2 {
		t.Errorf("expected 2 total logs, got %v", resp["total"])
	}
}

func TestAdminHandler_GetAuditLogs_Filtered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAudit := &mockAuditWithData{
		entries: []domain.AuditEntry{
			{Action: "license.activate", Target: "key1", Success: true},
			{Action: "payment.callback", Target: "order1", Success: true},
		},
	}

	adminHandler := NewAdminHandler(nil, mockAudit)
	r := gin.New()
	r.GET("/admin/audit", adminHandler.GetAuditLogs)

	req, _ := http.NewRequest("GET", "/admin/audit?action=license.activate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if int(resp["total"].(float64)) != 1 {
		t.Errorf("expected 1 filtered log, got %v", resp["total"])
	}

	logs := resp["logs"].([]interface{})
	if len(logs) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(logs))
	}
}

func TestAdminHandler_GetAuditLogs_Pagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var entries []domain.AuditEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, domain.AuditEntry{
			Action:  "test.action",
			Target:  "target",
			Success: true,
		})
	}

	mockAudit := &mockAuditWithData{entries: entries}
	adminHandler := NewAdminHandler(nil, mockAudit)
	r := gin.New()
	r.GET("/admin/audit", adminHandler.GetAuditLogs)

	req, _ := http.NewRequest("GET", "/admin/audit?limit=3&offset=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if int(resp["total"].(float64)) != 10 {
		t.Errorf("expected 10 total, got %v", resp["total"])
	}

	logs := resp["logs"].([]interface{})
	if len(logs) != 3 {
		t.Errorf("expected 3 logs in page, got %d", len(logs))
	}
}

func TestAdminHandler_GetAuditLogs_RedactsEmailActors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAudit := &mockAuditWithData{entries: []domain.AuditEntry{{
		Actor:   "writer@example.com",
		Action:  domain.AuditActionRegistrationSubmit,
		Target:  "writer@example.com",
		Details: "restore writer@example.com",
		Success: true,
	}}}
	adminHandler := NewAdminHandler(nil, mockAudit)
	r := gin.New()
	r.GET("/admin/audit", adminHandler.GetAuditLogs)

	req, _ := http.NewRequest("GET", "/admin/audit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "writer@example.com") || strings.Contains(w.Body.String(), `"Actor"`) {
		t.Fatalf("audit response leaked raw actor: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"actor"`) || !strings.Contains(w.Body.String(), `"masked"`) {
		t.Fatalf("expected redacted actor projection, got %s", w.Body.String())
	}
}
