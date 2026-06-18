package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeAccessAdminService struct {
	query  service.AccessAdminQuery
	result *service.AccessAccountList
}

func (f *fakeAccessAdminService) ListAccounts(ctx context.Context, query service.AccessAdminQuery) (*service.AccessAccountList, error) {
	f.query = query
	return f.result, nil
}

func TestAccessAdminHandler_ListAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccessAdminService{result: &service.AccessAccountList{Total: 1, Accounts: []service.AccessAccountRecord{{UserID: "usr_1", EmailMasked: "wr**er@example.com"}}}}
	handler := NewAccessAdminHandler(fake)
	r := gin.New()
	r.GET("/admin/access-accounts", handler.ListAccounts)

	req, _ := http.NewRequest(http.MethodGet, "/admin/access-accounts?email=writer@example.com&limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.query.Email != "writer@example.com" || fake.query.Limit != 5 {
		t.Fatalf("expected query passthrough, got %#v", fake.query)
	}
	if strings.Contains(w.Body.String(), `"email":"`) || strings.Contains(w.Body.String(), "writer@example.com") {
		t.Fatalf("response leaked raw email: %s", w.Body.String())
	}
	var resp service.AccessAccountList
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || resp.Accounts[0].EmailMasked != "wr**er@example.com" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}
