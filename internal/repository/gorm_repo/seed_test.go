package gorm_repo

import (
	"context"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProductCatalogReconcilerUsesBYOKCatalogAndHidesNonCheckoutProducts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:product_catalog_reconciler?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.Product{}); err != nil {
		t.Fatalf("migrate products: %v", err)
	}

	ctx := context.Background()
	repo := &ProductRepo{DB: db}
	if err := repo.Create(ctx, &domain.Product{Code: "editorial_studio_monthly", Name: "Legacy visible product", Price: 1900, Currency: "USD", Validity: "monthly", IsVisible: true}); err != nil {
		t.Fatalf("precreate legacy product: %v", err)
	}

	reconciler := service.NewProductCatalogReconciler(repo, service.DefaultProductCatalog())
	result, err := reconciler.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile catalog: %v", err)
	}
	if result.Created == 0 || result.Updated == 0 {
		t.Fatalf("expected catalog create and legacy update, got %#v", result)
	}

	visible, err := repo.List(ctx, true)
	if err != nil {
		t.Fatalf("list visible products: %v", err)
	}
	visibleCodes := make(map[string]bool, len(visible))
	for _, product := range visible {
		visibleCodes[product.Code] = true
	}
	for _, code := range []string{domain.SKUProOwnAIMonthly, domain.SKUProOwnAILifetime} {
		if !visibleCodes[code] {
			t.Fatalf("expected checkout SKU %s to be visible, got %#v", code, visibleCodes)
		}
	}
	for _, code := range []string{domain.PlanBasicOwnAI, "sub_monthly", "sub_yearly", "editorial_studio_monthly", "credits_600"} {
		if visibleCodes[code] {
			t.Fatalf("non-checkout product %s must be hidden from checkout catalog", code)
		}
		product, err := repo.GetByCode(ctx, code)
		if err != nil {
			t.Fatalf("product %s should remain readable for restore/legacy orders: %v", code, err)
		}
		if product.IsVisible {
			t.Fatalf("product %s should be hidden, got %#v", code, product)
		}
	}

	second, err := reconciler.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.Created != 0 || second.Updated != 0 {
		t.Fatalf("expected idempotent second reconcile, got %#v", second)
	}
}
