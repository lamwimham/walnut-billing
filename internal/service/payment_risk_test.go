package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

func TestPaymentRiskService_ResolvesOpenFlag(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["prf_1"] = &domain.PaymentRiskFlag{
		ID:              "prf_1",
		UserID:          "usr_1",
		Provider:        "creem",
		ProviderEventID: "evt_1",
		Severity:        domain.PaymentRiskSeverityCritical,
		Status:          domain.PaymentRiskStatusOpen,
		Note:            "provider dispute/chargeback event",
	}
	svc := NewPaymentRiskService(risks)

	flag, err := svc.ResolveFlag(context.Background(), ResolvePaymentRiskFlagInput{ID: " prf_1 ", ResolvedBy: "ops", Note: "verified legitimate customer"})
	if err != nil {
		t.Fatalf("resolve flag: %v", err)
	}
	if flag.Status != domain.PaymentRiskStatusResolved || flag.ResolvedAt == nil || flag.ResolvedBy != "ops" {
		t.Fatalf("expected resolved flag, got %#v", flag)
	}
	if !strings.Contains(flag.Note, "provider dispute") || !strings.Contains(flag.Note, "resolution: verified legitimate customer") {
		t.Fatalf("expected resolution note appended, got %q", flag.Note)
	}
}

func TestPaymentRiskService_ListAndGetMapRepositoryBoundaries(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["prf_1"] = &domain.PaymentRiskFlag{ID: "prf_1", UserID: "usr_1", Provider: "creem", Severity: domain.PaymentRiskSeverityHigh, Status: domain.PaymentRiskStatusOpen}
	risks.flags["prf_2"] = &domain.PaymentRiskFlag{ID: "prf_2", UserID: "usr_2", Provider: "creem", Severity: domain.PaymentRiskSeverityCritical, Status: domain.PaymentRiskStatusResolved}
	svc := NewPaymentRiskService(risks)

	flags, err := svc.ListFlags(context.Background(), repository.PaymentRiskFlagQuery{UserID: " usr_1 ", Status: domain.PaymentRiskStatusOpen})
	if err != nil {
		t.Fatalf("list flags: %v", err)
	}
	if len(flags) != 1 || flags[0].ID != "prf_1" {
		t.Fatalf("unexpected filtered flags: %#v", flags)
	}
	got, err := svc.GetFlag(context.Background(), "prf_1")
	if err != nil || got.ID != "prf_1" {
		t.Fatalf("unexpected get result flag=%#v err=%v", got, err)
	}
	_, err = svc.GetFlag(context.Background(), "missing")
	if !errors.Is(err, ErrPaymentRiskFlagNotFound) {
		t.Fatalf("expected not found mapping, got %v", err)
	}
}

func TestPaymentRiskResolveAllowsCheckoutAfterManualReview(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["prf_1"] = &domain.PaymentRiskFlag{ID: "prf_1", UserID: "usr_1", Severity: domain.PaymentRiskSeverityCritical, Status: domain.PaymentRiskStatusOpen}
	checkoutPolicy := NewPaymentRiskCheckoutPolicy(risks, DefaultCheckoutRiskPolicyConfig())
	checkoutSvc, _, products, users, gateway := newCheckoutTestServiceWithPolicies(checkoutPolicy)
	seedCheckoutUserAndProduct(users, products)

	_, err := checkoutSvc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "editorial_studio_monthly", Provider: "mock", IdempotencyKey: "checkout:risk-before-review"})
	if !errors.Is(err, ErrCheckoutBlockedByRisk) {
		t.Fatalf("expected risk block before review, got %v", err)
	}
	if _, err := NewPaymentRiskService(risks).ResolveFlag(context.Background(), ResolvePaymentRiskFlagInput{ID: "prf_1", ResolvedBy: "ops"}); err != nil {
		t.Fatalf("resolve risk: %v", err)
	}
	_, err = checkoutSvc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "editorial_studio_monthly", Provider: "mock", IdempotencyKey: "checkout:risk-after-review"})
	if err != nil {
		t.Fatalf("expected checkout after risk resolve, got %v", err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected one provider call after resolve, got %d", len(gateway.requests))
	}
}
