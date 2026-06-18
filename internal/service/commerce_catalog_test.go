package service

import (
	"context"
	"reflect"
	"testing"
	"walnut-billing/internal/domain"
)

func TestDefaultProductCatalogSeparatesPlanFromCheckoutSKUs(t *testing.T) {
	catalog := DefaultProductCatalog()
	basic, ok := catalog.ProductByCode(domain.PlanBasicOwnAI)
	if !ok {
		t.Fatalf("expected basic plan in catalog")
	}
	if basic.Kind != CatalogItemKindPlan || basic.CheckoutVisible {
		t.Fatalf("basic must be a non-checkout plan, got %#v", basic)
	}

	monthly, ok := catalog.ProductByCode(domain.SKUProOwnAIMonthly)
	if !ok {
		t.Fatalf("expected monthly BYOK SKU in catalog")
	}
	if monthly.Kind != CatalogItemKindSKU || !monthly.CheckoutVisible || monthly.Price != 500 || monthly.Currency != "USD" {
		t.Fatalf("unexpected monthly SKU definition: %#v", monthly)
	}
	if !reflect.DeepEqual(monthly.Access.GraceEntitlementIDs, CurrentAdvancedEntitlements()) {
		t.Fatalf("monthly BYOK grace should preserve current advanced entitlements, got %#v", monthly.Access)
	}

	legacy, ok := catalog.ProductByCode("editorial_studio_monthly")
	if !ok {
		t.Fatalf("expected legacy editorial SKU in catalog")
	}
	if legacy.Kind != CatalogItemKindLegacySKU || legacy.CheckoutVisible || !reflect.DeepEqual(legacy.Access.GraceEntitlementIDs, []string{domain.EntitlementEditorialStudio}) {
		t.Fatalf("legacy SKU should be hidden but restorable, got %#v", legacy)
	}
}

func TestDefaultFulfillmentRulesKeepBYOKAccessEntitlementsSeparateFromHostedCredits(t *testing.T) {
	catalog, err := NewStaticFulfillmentCatalog(DefaultFulfillmentRules()...)
	if err != nil {
		t.Fatalf("build fulfillment catalog: %v", err)
	}
	rules, err := catalog.RulesForSKU(domain.SKUProOwnAIMonthly)
	if err != nil {
		t.Fatalf("monthly BYOK rules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected two current BYOK access entitlement rules, got %d", len(rules))
	}
	for _, rule := range rules {
		if rule.Type == FulfillmentRuleGrantCredits {
			t.Fatalf("BYOK software SKU must not grant hosted-AI credits: %#v", rule)
		}
	}

	legacyRules, err := catalog.RulesForSKU("credits_600")
	if err != nil || len(legacyRules) != 1 || legacyRules[0].Type != FulfillmentRuleGrantCredits {
		t.Fatalf("legacy credit orders should remain fulfillable, rules=%#v err=%v", legacyRules, err)
	}
}

func TestCatalogSubscriptionAccessPolicyUsesCatalogGraceEntitlements(t *testing.T) {
	policy := DefaultSubscriptionAccessPolicy()
	byok, err := policy.GraceEntitlementIDs(context.Background(), &domain.Order{SKUCode: domain.SKUProOwnAIMonthly})
	if err != nil || !reflect.DeepEqual(byok, CurrentAdvancedEntitlements()) {
		t.Fatalf("expected BYOK current advanced grace entitlements, got %q err=%v", byok, err)
	}
	legacy, err := policy.GraceEntitlementIDs(context.Background(), &domain.Order{SKUCode: "editorial_studio_monthly"})
	if err != nil || !reflect.DeepEqual(legacy, []string{domain.EntitlementEditorialStudio}) {
		t.Fatalf("expected legacy editorial grace entitlement, got %q err=%v", legacy, err)
	}
	if _, err := policy.GraceEntitlementIDs(context.Background(), &domain.Order{SKUCode: domain.SKUProOwnAILifetime}); err != ErrSubscriptionGraceUnsupported {
		t.Fatalf("lifetime SKU should not receive subscription grace, got %v", err)
	}
}
