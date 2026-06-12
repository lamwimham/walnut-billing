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
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeCreditService struct {
	usageRecords []domain.UsageRecord
	reserveInput service.CreditReservationInput
}

func (f *fakeCreditService) AccountForUser(ctx context.Context, userID string) (*domain.CreditAccount, error) {
	return &domain.CreditAccount{ID: "cra_1", UserID: userID}, nil
}

func (f *fakeCreditService) Grant(ctx context.Context, input service.CreditGrantInput) (*service.CreditMutationResult, error) {
	return &service.CreditMutationResult{Account: &domain.CreditAccount{ID: "cra_1", UserID: input.UserID}}, nil
}

func (f *fakeCreditService) Reserve(ctx context.Context, input service.CreditReservationInput) (*service.CreditMutationResult, error) {
	f.reserveInput = input
	return &service.CreditMutationResult{
		Account: &domain.CreditAccount{ID: "cra_1", UserID: input.UserID, Balance: 70, Reserved: 30},
		Reservation: &domain.CreditReservation{
			ID:          "crr_1",
			UserID:      input.UserID,
			FeatureID:   input.FeatureID,
			Operation:   input.Operation,
			ExecutionID: input.ExecutionID,
			Amount:      input.Amount,
			Status:      domain.CreditReservationStatusPending,
		},
	}, nil
}

func (f *fakeCreditService) Commit(ctx context.Context, input service.CreditFinalizationInput) (*service.CreditMutationResult, error) {
	return nil, nil
}

func (f *fakeCreditService) Release(ctx context.Context, input service.CreditFinalizationInput) (*service.CreditMutationResult, error) {
	return nil, nil
}

func (f *fakeCreditService) ListTransactions(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error) {
	return nil, nil
}

func (f *fakeCreditService) ListUsageRecords(ctx context.Context, query service.UsageRecordQuery) ([]domain.UsageRecord, error) {
	return f.usageRecords, nil
}

func TestCreditHandler_ReserveAcceptsUsageMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fakeSvc := &fakeCreditService{}
	h := NewCreditHandler(fakeSvc, nil)
	r := gin.New()
	r.POST("/credits/reservations", h.Reserve)

	body := `{
		"user_id":"usr_1",
		"feature_id":"editorial.studio",
		"operation":"editorial.studio.run",
		"execution_id":"exec-1",
		"amount":30,
		"idempotency_key":"reserve-1",
		"project_id":"project-a",
		"document_id":"doc-1",
		"conversation_id":"conv-1",
		"client_message_id":"u1"
	}`
	req, _ := http.NewRequest(http.MethodPost, "/credits/reservations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}
	if fakeSvc.reserveInput.FeatureID != "editorial.studio" || fakeSvc.reserveInput.ExecutionID != "exec-1" {
		t.Fatalf("expected feature/execution metadata, got %+v", fakeSvc.reserveInput)
	}
	if fakeSvc.reserveInput.Metadata["project_id"] != "project-a" || fakeSvc.reserveInput.Metadata["client_message_id"] != "u1" {
		t.Fatalf("expected sanitized usage metadata, got %+v", fakeSvc.reserveInput.Metadata)
	}
}

func TestCreditHandler_ListUsageRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	fakeSvc := &fakeCreditService{usageRecords: []domain.UsageRecord{
		{
			ReservationID: "crr_1",
			UserID:        "usr_1",
			FeatureID:     "editorial.studio",
			Operation:     "editorial.studio.run",
			ExecutionID:   "exec-1",
			Amount:        30,
			Status:        domain.CreditReservationStatusCommitted,
			Metadata:      map[string]any{"project_id": "project-a"},
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}}
	h := NewCreditHandler(fakeSvc, nil)
	r := gin.New()
	r.GET("/admin/users/:user_id/credits/usage-records", h.ListUsageRecords)

	req, _ := http.NewRequest(http.MethodGet, "/admin/users/usr_1/credits/usage-records?operation=editorial.studio.run", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Total        int                  `json:"total"`
		UsageRecords []domain.UsageRecord `json:"usage_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Total != 1 || resp.UsageRecords[0].ReservationID != "crr_1" {
		t.Fatalf("unexpected usage records response: %+v", resp)
	}
}
