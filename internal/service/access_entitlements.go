package service

import "walnut-billing/internal/domain"

// CurrentAdvancedEntitlements is the product-access source of truth for the
// advanced rights that are wired to Walnut features today.
func CurrentAdvancedEntitlements() []string {
	return []string{
		domain.EntitlementEditorialStudio,
		domain.EntitlementCloudStorage,
	}
}

func IsCurrentAdvancedEntitlementID(entitlementID string) bool {
	return IsCurrentAccessEntitlementID(entitlementID)
}

func IsCurrentAccessEntitlementID(entitlementID string) bool {
	for _, current := range CurrentAdvancedEntitlements() {
		if entitlementID == current {
			return true
		}
	}
	return false
}
