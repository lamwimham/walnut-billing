package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
)

func TestAdminEntitlementProjectorMasksRegistrationPII(t *testing.T) {
	now := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	projector := NewAdminEntitlementProjector(NewAdminPrivacyProjector())
	result := projector.ProjectRegistration(domain.RegistrationRequest{
		ID:                   "reg_1",
		UserID:               "usr_1",
		Email:                "Writer@Example.COM",
		DisplayName:          "Writer",
		RequestedEntitlement: domain.EntitlementEditorialStudio,
		Status:               domain.RegistrationStatusPending,
		Note:                 "contact Writer@Example.COM",
		CreatedAt:            now,
	})
	if result.EmailMasked != "wr**er@example.com" || result.EmailDomain != "example.com" || result.EmailFingerprint == "" {
		t.Fatalf("expected masked email projection, got %#v", result)
	}
	if result.DisplayNameMasked != "W***" || result.CreatedAt == "" {
		t.Fatalf("expected masked display name and formatted time, got %#v", result)
	}
	payload, _ := json.Marshal(result)
	if strings.Contains(string(payload), "writer@example.com") || strings.Contains(string(payload), "Writer@Example.COM") {
		t.Fatalf("registration projection leaked raw email: %s", payload)
	}
}
