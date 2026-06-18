package service

import "walnut-billing/internal/domain"

type AdminRegistrationList struct {
	Total         int                       `json:"total"`
	Registrations []AdminRegistrationRecord `json:"registrations"`
}

type AdminRegistrationRecord struct {
	ID                   string `json:"id"`
	UserID               string `json:"user_id"`
	EmailMasked          string `json:"email_masked"`
	EmailFingerprint     string `json:"email_fingerprint"`
	EmailDomain          string `json:"email_domain"`
	DisplayNameMasked    string `json:"display_name_masked"`
	RequestedEntitlement string `json:"requested_entitlement"`
	Status               string `json:"status"`
	Source               string `json:"source"`
	DeviceID             string `json:"device_id"`
	Note                 string `json:"note"`
	ReviewNote           string `json:"review_note"`
	ReviewedBy           string `json:"reviewed_by"`
	ReviewedAt           string `json:"reviewed_at"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type AdminEntitlementProjector struct {
	privacy AdminPrivacyProjector
}

func NewAdminEntitlementProjector(privacy AdminPrivacyProjector) AdminEntitlementProjector {
	return AdminEntitlementProjector{privacy: privacy}
}

func (p AdminEntitlementProjector) ProjectRegistration(registration domain.RegistrationRequest) AdminRegistrationRecord {
	email := p.privacy.ProjectEmail(registration.Email)
	return AdminRegistrationRecord{
		ID:                   registration.ID,
		UserID:               registration.UserID,
		EmailMasked:          email.Masked,
		EmailFingerprint:     email.Fingerprint,
		EmailDomain:          email.Domain,
		DisplayNameMasked:    maskDisplayName(registration.DisplayName),
		RequestedEntitlement: registration.RequestedEntitlement,
		Status:               registration.Status,
		Source:               registration.Source,
		DeviceID:             p.privacy.RedactIdentifier(registration.DeviceID),
		Note:                 p.privacy.RedactFreeText(registration.Note),
		ReviewNote:           p.privacy.RedactFreeText(registration.ReviewNote),
		ReviewedBy:           p.privacy.RedactIdentifier(registration.ReviewedBy),
		ReviewedAt:           formatOptionalTime(registration.ReviewedAt),
		CreatedAt:            formatTime(registration.CreatedAt),
		UpdatedAt:            formatTime(registration.UpdatedAt),
	}
}

func (p AdminEntitlementProjector) ProjectRegistrationList(registrations []domain.RegistrationRequest) AdminRegistrationList {
	records := make([]AdminRegistrationRecord, 0, len(registrations))
	for _, registration := range registrations {
		records = append(records, p.ProjectRegistration(registration))
	}
	return AdminRegistrationList{Total: len(records), Registrations: records}
}
