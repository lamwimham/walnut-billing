package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

// mockAuditService is a no-op audit service for handler tests
type mockAuditService struct {
	entries []domain.AuditEntry
}

func (m *mockAuditService) Record(ctx context.Context, entry *domain.AuditEntry) {
	if entry != nil {
		m.entries = append(m.entries, *entry)
	}
}
func (m *mockAuditService) Query(ctx context.Context, query repository.AuditQuery) ([]domain.AuditEntry, int64, error) {
	return nil, 0, nil
}

var _ service.AuditService = (*mockAuditService)(nil)

// mockProvider for handler tests
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return "http://mock.pay/" + outTradeNo, nil
}
func (m *mockProvider) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, paidAmount int64, err error) {
	return params["out_trade_no"], "txn-123", 0, nil
}
func (m *mockProvider) BuildSuccessResponse() (contentType, body string) {
	return "application/json", `{"status":"ok"}`
}
func (m *mockProvider) BuildFailureResponse() (contentType, body string) {
	return "application/json", `{"status":"fail"}`
}

// setupConfigTestRouter creates a gin router with the config handler routes for testing
func setupConfigTestRouter(svc *payment.PaymentService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewPaymentConfigHandler(svc, &mockAuditService{})
	r.GET("/admin/payment/providers", h.GetProviderStatus)
	r.PUT("/admin/payment/wechat", h.UpdateWechatConfig)
	r.PUT("/admin/payment/alipay", h.UpdateAlipayConfig)
	r.PUT("/admin/payment/creem", h.UpdateCreemConfig)
	r.POST("/admin/payment/:provider/mock", h.SwitchToMock)
	r.POST("/admin/payment/import", h.ImportProviders)
	return r
}

func TestConfigHandler_GetProviderStatus(t *testing.T) {
	registry := payment.NewProviderRegistry()
	registry.Register("wechat", &mockProvider{name: "wechat"}, payment.ProviderStatus{SandboxMode: false})
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	req, _ := http.NewRequest("GET", "/admin/payment/providers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestConfigHandler_GetProviderStatusIncludesUnavailableProviders(t *testing.T) {
	registry := payment.NewProviderRegistry()
	registry.RegisterStatus("creem", payment.ProviderStatus{Status: "error", Error: "missing product map", SandboxMode: true})
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	req, _ := http.NewRequest("GET", "/admin/payment/providers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	var response struct {
		Providers map[string]payment.ProviderStatus `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	creem := response.Providers["creem"]
	if creem.Status != "error" || creem.Error != "missing product map" || !creem.SandboxMode {
		t.Fatalf("expected unavailable creem status in response, got %#v", creem)
	}
}

func TestConfigHandler_SwitchToMock(t *testing.T) {
	registry := payment.NewProviderRegistry()
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	// Switch wechat to mock
	req, _ := http.NewRequest("POST", "/admin/payment/wechat/mock", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}

	statuses := svc.GetProviderStatus()
	if s, ok := statuses["wechat"]; !ok {
		t.Error("expected wechat provider to be registered")
	} else {
		if !s.IsMock {
			t.Error("expected wechat to be a mock provider")
		}
	}
}

func TestConfigHandler_UpdateCreemConfig(t *testing.T) {
	registry := payment.NewProviderRegistry()
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	body := map[string]interface{}{
		"api_key":        "creem_test_key",
		"webhook_secret": "whsec_test",
		"sandbox":        true,
		"product_ids": map[string]string{
			"editorial_studio_monthly": "prod_studio",
		},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("PUT", "/admin/payment/creem", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}
	statuses := svc.GetProviderStatus()
	if s, ok := statuses["creem"]; !ok {
		t.Fatal("expected creem provider to be registered")
	} else if s.IsMock || !s.SandboxMode {
		t.Fatalf("expected real sandbox creem provider, got %#v", s)
	}
}

func TestConfigHandler_UpdateCreemConfigAuditsWithoutSecrets(t *testing.T) {
	registry := payment.NewProviderRegistry()
	svc := payment.NewPaymentService(nil, nil, registry)
	audit := &mockAuditService{}
	h := NewPaymentConfigHandler(svc, audit)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/admin/payment/creem",
		middleware.APIKeyAuthPrincipals([]middleware.AdminPrincipal{{Name: "ops", APIKey: "ops-secret", Permissions: []string{middleware.PermissionPaymentWrite}}}),
		h.UpdateCreemConfig,
	)

	body := map[string]any{
		"api_key":        "creem_test_should_not_leak",
		"webhook_secret": "whsec_should_not_leak",
		"sandbox":        true,
		"product_ids": map[string]string{
			"pro_own_ai_monthly": "prod_secret_should_not_leak",
		},
	}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", "/admin/payment/creem", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ops-secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(audit.entries) != 1 {
		t.Fatalf("expected one audit entry, got %#v", audit.entries)
	}
	entry := audit.entries[0]
	if entry.Actor != "ops" || entry.Action != domain.AuditActionConfigUpdate || entry.Target != "payment.creem" || !entry.Success {
		t.Fatalf("unexpected audit entry: %#v", entry)
	}
	for _, leaked := range []string{"creem_test_should_not_leak", "whsec_should_not_leak", "prod_secret_should_not_leak", "ops-secret"} {
		if strings.Contains(entry.Details, leaked) || strings.Contains(entry.Actor, leaked) {
			t.Fatalf("audit entry leaked secret %q: %#v", leaked, entry)
		}
	}
	if !strings.Contains(entry.Details, `"secret_fields_set":["api_key","webhook_secret"]`) {
		t.Fatalf("expected secret field names only, got %s", entry.Details)
	}
}

// TestUpdateWithInvalidKey ensures validation rejects bad keys
func TestConfigHandler_UpdateWechatConfig_InvalidKey(t *testing.T) {
	registry := payment.NewProviderRegistry()
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	body := map[string]interface{}{
		"mch_id":      "123456",
		"app_id":      "wx123",
		"serial_no":   "SN123",
		"api_v3_key":  "key123",
		"private_key": "not-a-valid-key",
		"sandbox":     true,
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("PUT", "/admin/payment/wechat", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid key, got %d", w.Code)
	}
}

func TestConfigHandler_ImportProviders(t *testing.T) {
	registry := payment.NewProviderRegistry()
	svc := payment.NewPaymentService(nil, nil, registry)
	router := setupConfigTestRouter(svc)

	// Import a mock provider via the proper structure
	// Since we don't have valid keys, we expect partial success/failure
	importPayload := map[string]interface{}{
		"wechat": map[string]interface{}{
			"mch_id": "123",
			// missing required fields -> will fail validation
		},
	}
	jsonBody, _ := json.Marshal(importPayload)

	req, _ := http.NewRequest("POST", "/admin/payment/import", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}
}
