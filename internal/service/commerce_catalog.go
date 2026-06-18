package service

import (
	"context"
	"errors"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidProductCatalog        = errors.New("invalid product catalog")
	ErrInvalidProductCatalogStore   = errors.New("invalid product catalog store")
	ErrSubscriptionGraceUnsupported = errors.New("subscription grace unsupported")
)

type CatalogItemKind string

const (
	CatalogItemKindPlan      CatalogItemKind = "plan"
	CatalogItemKindSKU       CatalogItemKind = "sku"
	CatalogItemKindLegacySKU CatalogItemKind = "legacy_sku"
)

type ProductAccessPolicyDefinition struct {
	GraceEntitlementIDs []string
}

// ProductDefinition is the billing-side commercial catalog record. Free plans
// can exist in the catalog without becoming checkout-visible products.
type ProductDefinition struct {
	Code            string
	Name            string
	Price           int64
	Currency        string
	Validity        string
	Kind            CatalogItemKind
	CheckoutVisible bool
	Access          ProductAccessPolicyDefinition
}

func (d ProductDefinition) DomainProduct() domain.Product {
	d = normalizeProductDefinition(d)
	return domain.Product{
		Code:      d.Code,
		Name:      d.Name,
		Price:     d.Price,
		Currency:  d.Currency,
		Validity:  d.Validity,
		IsVisible: d.CheckoutVisible,
	}
}

type ProductCatalog interface {
	Definitions() []ProductDefinition
	ProductByCode(code string) (ProductDefinition, bool)
}

type staticProductCatalog struct {
	definitions []ProductDefinition
	byCode      map[string]ProductDefinition
}

func NewStaticProductCatalog(definitions ...ProductDefinition) (ProductCatalog, error) {
	catalog := &staticProductCatalog{byCode: make(map[string]ProductDefinition)}
	for _, definition := range definitions {
		normalized := normalizeProductDefinition(definition)
		if normalized.Code == "" || normalized.Name == "" || normalized.Currency == "" || normalized.Validity == "" || normalized.Kind == "" {
			return nil, ErrInvalidProductCatalog
		}
		if _, exists := catalog.byCode[normalized.Code]; exists {
			return nil, ErrInvalidProductCatalog
		}
		catalog.definitions = append(catalog.definitions, normalized)
		catalog.byCode[normalized.Code] = normalized
	}
	return catalog, nil
}

func DefaultProductCatalog() ProductCatalog {
	return mustProductCatalog(NewStaticProductCatalog(defaultProductDefinitions()...))
}

func (c *staticProductCatalog) Definitions() []ProductDefinition {
	if c == nil {
		return nil
	}
	result := make([]ProductDefinition, len(c.definitions))
	copy(result, c.definitions)
	return result
}

func (c *staticProductCatalog) ProductByCode(code string) (ProductDefinition, bool) {
	if c == nil {
		return ProductDefinition{}, false
	}
	definition, ok := c.byCode[strings.TrimSpace(code)]
	return definition, ok
}

type CommerceCatalog interface {
	Products() ProductCatalog
	Entitlements() EntitlementCatalog
	FulfillmentRules() []FulfillmentRule
	SubscriptionAccessPolicy() SubscriptionAccessPolicy
}

type staticCommerceCatalog struct {
	products           ProductCatalog
	entitlements       EntitlementCatalog
	fulfillmentRules   []FulfillmentRule
	subscriptionAccess SubscriptionAccessPolicy
}

func DefaultCommerceCatalog() CommerceCatalog {
	products := DefaultProductCatalog()
	return &staticCommerceCatalog{
		products:           products,
		entitlements:       DefaultEntitlementCatalog(),
		fulfillmentRules:   DefaultFulfillmentRules(),
		subscriptionAccess: NewCatalogSubscriptionAccessPolicy(products, CurrentAdvancedEntitlements()),
	}
}

func (c *staticCommerceCatalog) Products() ProductCatalog {
	return c.products
}

func (c *staticCommerceCatalog) Entitlements() EntitlementCatalog {
	return c.entitlements
}

func (c *staticCommerceCatalog) FulfillmentRules() []FulfillmentRule {
	result := make([]FulfillmentRule, len(c.fulfillmentRules))
	copy(result, c.fulfillmentRules)
	return result
}

func (c *staticCommerceCatalog) SubscriptionAccessPolicy() SubscriptionAccessPolicy {
	return c.subscriptionAccess
}

type ProductCatalogRepository interface {
	Create(ctx context.Context, product *domain.Product) error
	GetByCode(ctx context.Context, code string) (*domain.Product, error)
	Update(ctx context.Context, product *domain.Product) error
}

type ProductCatalogReconciler struct {
	products ProductCatalogRepository
	catalog  ProductCatalog
}

type ProductCatalogReconcileResult struct {
	Created   int
	Updated   int
	Unchanged int
}

func NewProductCatalogReconciler(products ProductCatalogRepository, catalog ProductCatalog) *ProductCatalogReconciler {
	return &ProductCatalogReconciler{products: products, catalog: catalog}
}

func (r *ProductCatalogReconciler) Reconcile(ctx context.Context) (ProductCatalogReconcileResult, error) {
	if r == nil || r.products == nil || r.catalog == nil {
		return ProductCatalogReconcileResult{}, ErrInvalidProductCatalogStore
	}
	result := ProductCatalogReconcileResult{}
	for _, definition := range r.catalog.Definitions() {
		desired := definition.DomainProduct()
		existing, err := r.products.GetByCode(ctx, desired.Code)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				return result, err
			}
			if err := r.products.Create(ctx, &desired); err != nil {
				return result, err
			}
			result.Created++
			continue
		}
		if productNeedsReconcile(existing, desired) {
			desired.Code = existing.Code
			if err := r.products.Update(ctx, &desired); err != nil {
				return result, err
			}
			result.Updated++
			continue
		}
		result.Unchanged++
	}
	return result, nil
}

type SubscriptionAccessPolicy interface {
	GraceEntitlementIDs(ctx context.Context, order *domain.Order) ([]string, error)
}

type catalogSubscriptionAccessPolicy struct {
	catalog                      ProductCatalog
	legacyFallbackEntitlementIDs []string
}

func DefaultSubscriptionAccessPolicy() SubscriptionAccessPolicy {
	return NewCatalogSubscriptionAccessPolicy(DefaultProductCatalog(), CurrentAdvancedEntitlements())
}

func NewCatalogSubscriptionAccessPolicy(catalog ProductCatalog, legacyFallbackEntitlementIDs []string) SubscriptionAccessPolicy {
	return &catalogSubscriptionAccessPolicy{catalog: catalog, legacyFallbackEntitlementIDs: normalizeStringSet(legacyFallbackEntitlementIDs)}
}

func (p *catalogSubscriptionAccessPolicy) GraceEntitlementIDs(ctx context.Context, order *domain.Order) ([]string, error) {
	_ = ctx
	if p == nil || order == nil || strings.TrimSpace(order.SKUCode) == "" {
		return nil, ErrSubscriptionGraceUnsupported
	}
	if p.catalog != nil {
		if definition, ok := p.catalog.ProductByCode(order.SKUCode); ok {
			entitlementIDs := normalizeStringSet(definition.Access.GraceEntitlementIDs)
			if len(entitlementIDs) == 0 {
				return nil, ErrSubscriptionGraceUnsupported
			}
			return entitlementIDs, nil
		}
	}
	if len(p.legacyFallbackEntitlementIDs) > 0 {
		result := make([]string, len(p.legacyFallbackEntitlementIDs))
		copy(result, p.legacyFallbackEntitlementIDs)
		return result, nil
	}
	return nil, ErrSubscriptionGraceUnsupported
}

func DefaultFulfillmentRules() []FulfillmentRule {
	rules := make([]FulfillmentRule, 0, len(BYOKOwnAIFulfillmentRules())+len(LegacyFulfillmentRules()))
	rules = append(rules, BYOKOwnAIFulfillmentRules()...)
	rules = append(rules, LegacyFulfillmentRules()...)
	return rules
}

func BYOKOwnAIFulfillmentRules() []FulfillmentRule {
	return []FulfillmentRule{
		{
			ID:            "pro_own_ai_monthly:editorial_studio",
			SKUCode:       domain.SKUProOwnAIMonthly,
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementEditorialStudio,
			Duration:      "monthly",
		},
		{
			ID:            "pro_own_ai_monthly:cloud_storage",
			SKUCode:       domain.SKUProOwnAIMonthly,
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementCloudStorage,
			Duration:      "monthly",
		},
		{
			ID:            "pro_own_ai_lifetime:editorial_studio",
			SKUCode:       domain.SKUProOwnAILifetime,
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementEditorialStudio,
			Duration:      "lifetime",
		},
		{
			ID:            "pro_own_ai_lifetime:cloud_storage",
			SKUCode:       domain.SKUProOwnAILifetime,
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementCloudStorage,
			Duration:      "lifetime",
		},
	}
}

func LegacyFulfillmentRules() []FulfillmentRule {
	return []FulfillmentRule{
		{
			ID:            "editorial_studio_monthly:entitlement",
			SKUCode:       "editorial_studio_monthly",
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementEditorialStudio,
			Duration:      "monthly",
		},
		{
			ID:                "editorial_studio_monthly:credits_600",
			SKUCode:           "editorial_studio_monthly",
			Type:              FulfillmentRuleGrantCredits,
			CreditsAmount:     600,
			CreditsBucketType: domain.CreditBucketTypeSubscriptionPeriod,
			Duration:          "monthly",
		},
		{
			ID:                "credits_600:credits",
			SKUCode:           "credits_600",
			Type:              FulfillmentRuleGrantCredits,
			CreditsAmount:     600,
			CreditsBucketType: domain.CreditBucketTypeTopup,
		},
	}
}

func defaultProductDefinitions() []ProductDefinition {
	return []ProductDefinition{
		{
			Code:            domain.PlanBasicOwnAI,
			Name:            "Walnut Basic Own AI",
			Price:           0,
			Currency:        "USD",
			Validity:        "lifetime",
			Kind:            CatalogItemKindPlan,
			CheckoutVisible: false,
		},
		{
			Code:            domain.SKUProOwnAIMonthly,
			Name:            "Walnut Pro Own AI Monthly",
			Price:           500,
			Currency:        "USD",
			Validity:        "monthly",
			Kind:            CatalogItemKindSKU,
			CheckoutVisible: true,
			Access: ProductAccessPolicyDefinition{
				GraceEntitlementIDs: CurrentAdvancedEntitlements(),
			},
		},
		{
			Code:            domain.SKUProOwnAILifetime,
			Name:            "Walnut Pro Own AI Lifetime",
			Price:           9900,
			Currency:        "USD",
			Validity:        "lifetime",
			Kind:            CatalogItemKindSKU,
			CheckoutVisible: true,
		},
		legacyProductDefinition("pro", "walnut Pro (legacy buyout)", 12800, "CNY", "lifetime", ""),
		legacyProductDefinition("std", "walnut Standard (legacy buyout)", 6800, "CNY", "lifetime", ""),
		legacyProductDefinition("sub_monthly", "walnut AI Subscription Monthly (legacy)", 1500, "CNY", "monthly", ""),
		legacyProductDefinition("sub_yearly", "walnut AI Subscription Yearly (legacy)", 15000, "CNY", "yearly", ""),
		legacyProductDefinition("editorial_studio_monthly", "Walnut Editorial Studio Monthly (legacy)", 1900, "USD", "monthly", domain.EntitlementEditorialStudio),
		legacyProductDefinition("credits_600", "Walnut Credits 600 (legacy)", 990, "USD", "lifetime", ""),
	}
}

func legacyProductDefinition(code string, name string, price int64, currency string, validity string, graceEntitlementID string) ProductDefinition {
	return ProductDefinition{
		Code:            code,
		Name:            name,
		Price:           price,
		Currency:        currency,
		Validity:        validity,
		Kind:            CatalogItemKindLegacySKU,
		CheckoutVisible: false,
		Access: ProductAccessPolicyDefinition{
			GraceEntitlementIDs: []string{graceEntitlementID},
		},
	}
}

func normalizeProductDefinition(definition ProductDefinition) ProductDefinition {
	definition.Code = strings.TrimSpace(definition.Code)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Currency = strings.TrimSpace(definition.Currency)
	definition.Validity = strings.TrimSpace(definition.Validity)
	definition.Kind = CatalogItemKind(strings.TrimSpace(string(definition.Kind)))
	definition.Access.GraceEntitlementIDs = normalizeStringSet(definition.Access.GraceEntitlementIDs)
	if definition.Currency == "" {
		definition.Currency = "USD"
	}
	if definition.Validity == "" {
		definition.Validity = "lifetime"
	}
	return definition
}

func mustProductCatalog(catalog ProductCatalog, err error) ProductCatalog {
	if err != nil {
		panic(err)
	}
	return catalog
}

func productNeedsReconcile(existing *domain.Product, desired domain.Product) bool {
	return existing == nil ||
		existing.Name != desired.Name ||
		existing.Price != desired.Price ||
		existing.Currency != desired.Currency ||
		existing.Validity != desired.Validity ||
		existing.IsVisible != desired.IsVisible
}
