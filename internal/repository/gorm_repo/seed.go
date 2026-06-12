package gorm_repo

import (
	"context"
	"log"
	"walnut-billing/internal/domain"

	"gorm.io/gorm"
)

// SeedProducts inserts default walnut products if they don't already exist.
func SeedProducts(db *gorm.DB) {
	products := []domain.Product{
		{
			Code:      "pro",
			Name:      "walnut 专业版（买断）",
			Price:     12800, // ¥128 in cents
			Validity:  "lifetime",
			IsVisible: true,
		},
		{
			Code:      "std",
			Name:      "walnut 标准版（买断）",
			Price:     6800, // ¥68 in cents
			Validity:  "lifetime",
			IsVisible: true,
		},
		{
			Code:      "sub_monthly",
			Name:      "walnut AI 订阅（月付）",
			Price:     1500, // ¥15/month in cents
			Validity:  "monthly",
			IsVisible: true,
		},
		{
			Code:      "sub_yearly",
			Name:      "walnut AI 订阅（年付）",
			Price:     15000, // ¥150/year in cents
			Validity:  "yearly",
			IsVisible: true,
		},
		{
			Code:      "editorial_studio_monthly",
			Name:      "Walnut 编辑部工作室（月度）",
			Price:     1900, // ¥19/month in cents
			Validity:  "monthly",
			IsVisible: true,
		},
		{
			Code:      "credits_600",
			Name:      "Walnut Credits 600",
			Price:     990, // ¥9.9 in cents
			Validity:  "lifetime",
			IsVisible: true,
		},
	}

	ctx := context.Background()
	repo := &ProductRepo{DB: db}

	for _, p := range products {
		_, err := repo.GetByCode(ctx, p.Code)
		if err != nil {
			// Product doesn't exist, create it
			if err := repo.Create(ctx, &p); err != nil {
				log.Printf("[seed] failed to create product %s: %v", p.Code, err)
			} else {
				log.Printf("[seed] created product: %s (%s) - ¥%.2f", p.Code, p.Validity, float64(p.Price)/100)
			}
		}
	}
}
