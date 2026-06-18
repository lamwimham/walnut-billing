package domain

import "time"

// AuditEntry represents an immutable record of a system action.
// Used for security, compliance, and troubleshooting.
type AuditEntry struct {
	ID        uint      `gorm:"primaryKey"`
	Timestamp time.Time `gorm:"index"`
	Actor     string    `gorm:"size:64;index"` // IP, API Key, or "system"
	Action    string    `gorm:"size:32;index"` // e.g. "license.activate", "payment.callback"
	Target    string    `gorm:"size:128"`      // e.g. license key, order ID
	Details   string    `gorm:"type:text"`     // JSON payload or brief message
	IPAddress string    `gorm:"size:45"`       // Client IP
	Success   bool      `gorm:"index"`         // Whether the action succeeded
}

// TableName overrides the table name for AuditEntry.
func (AuditEntry) TableName() string {
	return "audit_logs"
}

// Audit action constants
const (
	AuditActionLicenseActivate            = "license.activate"
	AuditActionLicenseDeactivate          = "license.deactivate"
	AuditActionLicenseVerify              = "license.verify"
	AuditActionPaymentCallback            = "payment.callback"
	AuditActionOrderCreate                = "order.create"
	AuditActionConfigUpdate               = "config.update"
	AuditActionAdminQuery                 = "admin.query"
	AuditActionRegistrationSubmit         = "registration.submit"
	AuditActionRegistrationReview         = "registration.review"
	AuditActionAccessLoginChallengeCreate = "access.login_challenge.create"
	AuditActionAccessLoginChallengeVerify = "access.login_challenge.verify"
	AuditActionAccessDeviceRevoke         = "access.device.revoke"
	AuditActionEntitlementGrant           = "entitlement.grant"
	AuditActionCreditGrant                = "credit.grant"
	AuditActionCreditReserve              = "credit.reserve"
	AuditActionCreditCommit               = "credit.commit"
	AuditActionCreditRelease              = "credit.release"
	AuditActionCreditExpire               = "credit.expire"
	AuditActionPaymentRiskResolve         = "payment_risk.resolve"
)
